package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AttachmentUploadInitReceipt is the immutable acknowledgement of admission.
// It does not assert that the upload remains active or recreate an expired row.
type AttachmentUploadInitReceipt struct {
	UploadID    string `json:"upload_id"`
	ChunkSize   int64  `json:"chunk_size"`
	TotalChunks int    `json:"total_chunks"`
}

// AttachmentUploadInitPolicy is supplied by the existing API policy owner.
// Requested ChunkSize zero stays in the request identity, before this default.
type AttachmentUploadInitPolicy struct {
	DefaultChunkSize int64
	MinChunkSize     int64
	MaxChunkSize     int64
}

var (
	ErrAttachmentInitWriteRejected  = errors.New("attachment admission write rejected")
	ErrAttachmentInitOutcomeUnknown = errors.New("attachment admission outcome is unknown")
)

const attachmentInitOperation = "artifact.upload.init"
const attachmentInitReadbackTimeout = 2 * time.Second

type attachmentInitRejection struct{ cause error }

func (e *attachmentInitRejection) Error() string { return e.cause.Error() }
func (e *attachmentInitRejection) Unwrap() error { return e.cause }
func (e *attachmentInitRejection) Is(target error) bool {
	return target == ErrAttachmentInitWriteRejected
}

func rejectAttachmentInit(err error) error { return &attachmentInitRejection{cause: err} }

type attachmentInitIdentity struct{ owner, key, requestHash string }

func attachmentInitRequestIdentity(ctx context.Context, operationID string, input InitAttachmentUploadInput) (attachmentInitIdentity, error) {
	if ctx == nil {
		return attachmentInitIdentity{}, rejectAttachmentInit(ErrInvalidMutationIdempotencyKey)
	}
	parsed, err := uuid.Parse(operationID)
	if err != nil || parsed.String() != operationID {
		return attachmentInitIdentity{}, rejectAttachmentInit(ErrInvalidMutationIdempotencyKey)
	}
	if existing := ctx.Value(mutationIdempotencyContextKey{}); existing != nil {
		identity, present := existing.(mutationIdempotencyIdentity)
		if !present || identity.err != nil || identity.key != operationID {
			return attachmentInitIdentity{}, rejectAttachmentInit(ErrInvalidMutationIdempotencyKey)
		}
	}
	owner := strings.TrimSpace(input.UserID)
	if owner == "" {
		return attachmentInitIdentity{}, rejectAttachmentInit(errors.New("attachment user id is required"))
	}
	requestHash, err := mutationRequestHash(input)
	if err != nil {
		return attachmentInitIdentity{}, fmt.Errorf("%w: %w", ErrAttachmentInitOutcomeUnknown, err)
	}
	// A generic context helper can generate its own UUID for an empty input.
	// Only this explicit, validated caller argument proves admission identity.
	return attachmentInitIdentity{owner: owner, key: operationID, requestHash: requestHash}, nil
}

func (s *Store) InitAttachmentUploadWithReceipt(ctx context.Context, operationID string, rawInput InitAttachmentUploadInput, policy AttachmentUploadInitPolicy) (AttachmentUploadInitReceipt, error) {
	identity, err := attachmentInitRequestIdentity(ctx, operationID, rawInput)
	if err != nil {
		return AttachmentUploadInitReceipt{}, err
	}
	// Bind only after a nonempty canonical caller ID and any existing context
	// identity agree. This cannot invoke the generic helper's empty fallback.
	ctx = WithMutationIdempotencyKey(ctx, identity.key)
	if s == nil || s.db == nil {
		return AttachmentUploadInitReceipt{}, fmt.Errorf("%w: workspace store is closed", ErrAttachmentInitOutcomeUnknown)
	}
	s.attachmentMu.Lock()
	defer s.attachmentMu.Unlock()
	var receipt AttachmentUploadInitReceipt
	if found, err := s.lookupAttachmentInitReceipt(ctx, identity, &receipt); err != nil {
		if errors.Is(err, ErrMutationIdempotencyConflict) {
			return AttachmentUploadInitReceipt{}, err
		}
		return AttachmentUploadInitReceipt{}, fmt.Errorf("%w: %w", ErrAttachmentInitOutcomeUnknown, err)
	} else if found {
		return receipt, nil
	}
	input := rawInput
	if policy.MinChunkSize <= 0 || policy.MaxChunkSize < policy.MinChunkSize || policy.MaxChunkSize > maxAttachmentChunk || policy.DefaultChunkSize < policy.MinChunkSize || policy.DefaultChunkSize > policy.MaxChunkSize {
		return s.resolveAttachmentInitFailure(ctx, identity, rejectAttachmentInit(errors.New("attachment chunk policy is invalid")))
	}
	if input.ChunkSize == 0 {
		input.ChunkSize = policy.DefaultChunkSize
	}
	if input.ChunkSize < policy.MinChunkSize || input.ChunkSize > policy.MaxChunkSize {
		return s.resolveAttachmentInitFailure(ctx, identity, rejectAttachmentInit(errors.New("attachment requested chunk size is outside policy")))
	}
	input, chunks, err := normalizeAttachmentUploadInput(input)
	if err != nil {
		return s.resolveAttachmentInitFailure(ctx, identity, rejectAttachmentInit(err))
	}
	if err := s.expireStaleAttachmentUploads(ctx); err != nil {
		return s.resolveAttachmentInitFailure(ctx, identity, err)
	}
	owned, err := s.ProjectOwnedBy(input.ProjectID, input.UserID)
	if err != nil {
		return s.resolveAttachmentInitFailure(ctx, identity, err)
	}
	if !owned {
		return s.resolveAttachmentInitFailure(ctx, identity, rejectAttachmentInit(sql.ErrNoRows))
	}
	if err := s.checkAttachmentUploadSpace(ctx, input.TotalSize); err != nil {
		var capacity *InsufficientAttachmentSpaceError
		if errors.As(err, &capacity) {
			err = rejectAttachmentInit(err)
		}
		return s.resolveAttachmentInitFailure(ctx, identity, err)
	}
	upload := newAttachmentUpload(input, chunks, s.now().UTC())
	uploadDir := filepath.Join(s.blobRoot, "uploads", upload.ID)
	// Exclusive creation proves ownership of this one directory. Cleanup below
	// can remove only this attempt's empty directory, never a chunk tree.
	if err := os.Mkdir(uploadDir, 0o700); err != nil {
		return s.resolveAttachmentInitFailure(ctx, identity, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_ = os.Remove(uploadDir) // No transaction or SQL write was started.
		return s.resolveAttachmentInitFailure(ctx, identity, err)
	}
	commitAttempted := false
	err = func() error {
		found, err := lookupMutationResultTx(ctx, tx, identity.owner, identity.key, attachmentInitOperation, identity.requestHash, &receipt)
		if err != nil {
			return err
		}
		if found {
			return validateAttachmentInitReceipt(receipt)
		}
		// Use only this transaction while the single write connection is held.
		if err := requireProjectOwnerTx(ctx, tx, upload.ProjectID, upload.UserID); err != nil {
			return err
		}
		if err := insertAttachmentUploadRow(ctx, tx, upload); err != nil {
			return err
		}
		receipt = AttachmentUploadInitReceipt{UploadID: upload.ID, ChunkSize: upload.ChunkSize, TotalChunks: upload.ExpectedChunks}
		if err := insertMutationResultTx(ctx, tx, identity.owner, identity.key, attachmentInitOperation, identity.requestHash, receipt, upload.CreatedAt); err != nil {
			return err
		}
		commitAttempted = true
		return tx.Commit()
	}()
	rollbackErr := tx.Rollback()
	if !commitAttempted && rollbackErr == nil {
		_ = os.Remove(uploadDir)
	}
	if err != nil {
		return s.resolveAttachmentInitFailure(ctx, identity, err)
	}
	return receipt, nil
}

func validateAttachmentInitReceipt(receipt AttachmentUploadInitReceipt) error {
	parsed, err := uuid.Parse(receipt.UploadID)
	if err != nil || parsed.String() != receipt.UploadID || receipt.ChunkSize <= 0 || receipt.TotalChunks <= 0 {
		return errors.New("persisted attachment admission receipt is invalid")
	}
	return nil
}

func (s *Store) lookupAttachmentInitReceipt(ctx context.Context, identity attachmentInitIdentity, receipt *AttachmentUploadInitReceipt) (bool, error) {
	found, err := s.lookupMutationResult(ctx, identity.owner, identity.key, attachmentInitOperation, identity.requestHash, receipt)
	if err == nil && found {
		err = validateAttachmentInitReceipt(*receipt)
	}
	return found, err
}

func (s *Store) resolveAttachmentInitFailure(ctx context.Context, identity attachmentInitIdentity, failure error) (AttachmentUploadInitReceipt, error) {
	if errors.Is(failure, ErrMutationIdempotencyConflict) {
		return AttachmentUploadInitReceipt{}, failure
	}
	if ctx.Err() != nil {
		return AttachmentUploadInitReceipt{}, fmt.Errorf("%w: %w", ErrAttachmentInitOutcomeUnknown, errors.Join(failure, ctx.Err(), context.Cause(ctx)))
	}
	// One exact readback under the original live context, never an allocation
	// retry or a detached write. An absent receipt after a DB error is unknown.
	readCtx, cancel := context.WithTimeout(ctx, attachmentInitReadbackTimeout)
	defer cancel()
	var receipt AttachmentUploadInitReceipt
	found, readErr := s.lookupAttachmentInitReceipt(readCtx, identity, &receipt)
	if errors.Is(readErr, ErrMutationIdempotencyConflict) {
		return AttachmentUploadInitReceipt{}, readErr
	}
	if readErr == nil && found {
		return receipt, nil
	}
	if readErr == nil && !found && errors.Is(failure, ErrAttachmentInitWriteRejected) {
		return AttachmentUploadInitReceipt{}, failure
	}
	return AttachmentUploadInitReceipt{}, fmt.Errorf("%w: %w", ErrAttachmentInitOutcomeUnknown, errors.Join(failure, readErr))
}
