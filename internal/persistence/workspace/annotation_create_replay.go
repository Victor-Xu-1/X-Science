package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrAnnotationCommittedResultMissing = errors.New("committed annotation result is missing")
	ErrAnnotationCreateOutcomeUnknown   = errors.New("annotation create outcome is unknown")
)

const annotationCreateOperation = "annotation.create"
const annotationCreateReadbackTimeout = 2 * time.Second

type annotationCreateResult struct {
	AnnotationID string `json:"annotationId"`
}
type annotationCreateMutation struct{ owner, key, requestHash string }

func newAnnotationCreateMutation(ctx context.Context, input CreateAnnotationInput, owner string) (annotationCreateMutation, CreateAnnotationInput, error) {
	if ctx == nil {
		return annotationCreateMutation{}, input, fmt.Errorf("%w: request context is required", ErrAnnotationCreateOutcomeUnknown)
	}
	if err := mutationIdempotencyError(ctx); err != nil {
		return annotationCreateMutation{}, input, err
	}
	owner = strings.TrimSpace(owner)
	input.ProjectID, input.TargetKind, input.TargetKey = strings.TrimSpace(input.ProjectID), strings.TrimSpace(input.TargetKind), strings.TrimSpace(input.TargetKey)
	input.ContentChecksum = strings.TrimSpace(input.ContentChecksum)
	if owner == "" || input.TargetKind == "" || input.TargetKey == "" || input.Body == nil {
		return annotationCreateMutation{}, input, errors.New("annotation owner, target kind, target key, and body are required")
	}
	// Freeze nested caller values too. Hash the complete normalized request,
	// before generated IDs/labels/timestamps decorate the stored note.
	raw, err := json.Marshal(input.Body)
	if err != nil {
		return annotationCreateMutation{}, input, fmt.Errorf("marshal annotation request: %w", err)
	}
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber() // Freeze JSON numbers without rounding large typed integers.
	if err := decoder.Decode(&body); err != nil {
		return annotationCreateMutation{}, input, err
	}
	input.Body = body
	requestHash, err := mutationRequestHash(input)
	if err != nil {
		return annotationCreateMutation{}, input, err
	}
	// Capture once: legacy callers have independent keys; explicit callers
	// retain the same actor/key across all label attempts and bounded readback.
	return annotationCreateMutation{owner: owner, key: mutationIdempotencyKey(ctx), requestHash: requestHash}, input, nil
}

func loadAnnotationCreateResult(ctx context.Context, tx *sql.Tx, identity annotationCreateMutation, input CreateAnnotationInput, result annotationCreateResult) (Annotation, error) {
	// A receipt never grants access. Revalidate current source/owner before a
	// missing result can become scoped410, including Library Trash/revocation.
	if _, _, err := annotationCreateScope(ctx, tx, input, identity.owner); err != nil {
		return Annotation{}, err
	}
	if result.AnnotationID == "" {
		return Annotation{}, errors.New("persisted annotation receipt has no identity")
	}
	annotation, err := scanAnnotation(tx.QueryRowContext(ctx, annotationSelect+` WHERE id = ?`, result.AnnotationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Annotation{}, ErrAnnotationCommittedResultMissing
	}
	if err != nil {
		return Annotation{}, err
	}
	if annotation.ProjectID != input.ProjectID || annotation.TargetKind != input.TargetKind || annotation.TargetKey != input.TargetKey || annotation.ContentChecksum != input.ContentChecksum {
		return Annotation{}, ErrAnnotationReadSource
	}
	if err := requireAnnotationOwner(ctx, tx, annotation.ID, identity.owner); err != nil {
		return Annotation{}, err
	}
	// Return current contents. Replaying creation never overwrites later edits.
	return annotation, nil
}

func (s *Store) readAnnotationCreateResult(ctx context.Context, identity annotationCreateMutation, input CreateAnnotationInput) (Annotation, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Annotation{}, false, err
	}
	defer tx.Rollback()
	var result annotationCreateResult
	found, err := lookupMutationResultTx(ctx, tx, identity.owner, identity.key, annotationCreateOperation, identity.requestHash, &result)
	if err != nil {
		return Annotation{}, false, err
	}
	if !found {
		return Annotation{}, false, tx.Commit()
	}
	annotation, err := loadAnnotationCreateResult(ctx, tx, identity, input, result)
	if err != nil {
		return Annotation{}, true, err
	}
	return annotation, true, tx.Commit()
}

func (s *Store) resolveAnnotationCreateError(ctx context.Context, identity annotationCreateMutation, input CreateAnnotationInput, failure error) (Annotation, error) {
	if errors.Is(failure, ErrMutationIdempotencyConflict) || errors.Is(failure, ErrAnnotationCommittedResultMissing) || errors.Is(failure, ErrAnnotationReadSource) {
		return Annotation{}, failure
	}
	if ctx.Err() != nil {
		return Annotation{}, fmt.Errorf("%w: %w", ErrAnnotationCreateOutcomeUnknown, errors.Join(failure, ctx.Err(), context.Cause(ctx)))
	}
	// One finite receipt read, on the original context/key. Never detach a
	// retrying write or invent a replacement key after uncertain commit.
	readCtx, cancel := context.WithTimeout(ctx, annotationCreateReadbackTimeout)
	defer cancel()
	annotation, found, err := s.readAnnotationCreateResult(readCtx, identity, input)
	if err == nil && found {
		return annotation, nil
	}
	if errors.Is(err, ErrMutationIdempotencyConflict) || errors.Is(err, ErrAnnotationCommittedResultMissing) || errors.Is(err, ErrAnnotationReadSource) {
		return Annotation{}, err
	}
	return Annotation{}, fmt.Errorf("%w: %w", ErrAnnotationCreateOutcomeUnknown, errors.Join(failure, err))
}
