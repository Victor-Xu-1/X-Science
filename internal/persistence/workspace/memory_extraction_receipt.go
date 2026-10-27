package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type savedExtractionReceipt struct {
	inputDigest string
	planDigest  string
	result      ExtractionCommitResult
}

type extractionReceiptBinding uint8

const (
	extractionReceiptStrict extractionReceiptBinding = iota
	extractionReceiptReadOnly
)

func readExtractionReceiptTx(ctx context.Context, tx memoryRowQueryer, identity extractionSourceIdentity, binding extractionReceiptBinding) (savedExtractionReceipt, bool, error) {
	var saved savedExtractionReceipt
	var owner, project, frame, incarnation, sourceJSON, sourceDigest, resultJSON string
	err := tx.QueryRowContext(ctx, `SELECT owner_user_id,project_id,frame_id,frame_incarnation_id,
		source_json,source_sha256,input_sha256,plan_sha256,result_json FROM memory_extraction_receipts WHERE source_key=?`, identity.key).
		Scan(&owner, &project, &frame, &incarnation, &sourceJSON, &sourceDigest, &saved.inputDigest, &saved.planDigest, &resultJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return saved, false, nil
	}
	if err != nil {
		return savedExtractionReceipt{}, false, fmt.Errorf("read memory extraction receipt: %w", err)
	}
	if owner != identity.source.OwnerUserID || project != identity.source.ProjectID || frame != identity.source.FrameID ||
		incarnation != identity.incarnation || extractionDigest([]byte(sourceJSON)) != sourceDigest {
		return savedExtractionReceipt{}, false, ErrExtractionSourceChanged
	}
	if err := validateExtractionReceiptSource(identity, sourceJSON, sourceDigest, binding); err != nil {
		return savedExtractionReceipt{}, false, err
	}
	decoder := json.NewDecoder(bytes.NewBufferString(resultJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved.result) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) ||
		saved.result.Replayed || saved.result.InputSHA256 != saved.inputDigest ||
		normalizedExtractionDigest(saved.inputDigest) != saved.inputDigest ||
		normalizedExtractionDigest(saved.planDigest) != saved.planDigest || saved.result.Gate == "" ||
		saved.result.CursorBefore < 0 || saved.result.CursorAfter < 0 || saved.result.DeltaCount < 0 {
		return savedExtractionReceipt{}, false, ErrExtractionPlanConflict
	}
	if saved.result.ModeDecision.Validate(identity.source) != nil {
		return savedExtractionReceipt{}, false, ErrExtractionPlanConflict
	}
	return saved, true, nil
}

func validateExtractionReceiptSource(identity extractionSourceIdentity, sourceJSON, sourceDigest string, binding extractionReceiptBinding) error {
	var source CompletedExtractionSource
	decoder := json.NewDecoder(bytes.NewBufferString(sourceJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&source) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return ErrExtractionSourceChanged
	}
	stored, err := identifyExtractionSource(source)
	if err != nil || stored.key != identity.key || stored.encoded != sourceJSON || stored.digest != sourceDigest ||
		stored.source.OwnerUserID != identity.source.OwnerUserID || stored.source.ProjectID != identity.source.ProjectID ||
		stored.source.FrameID != identity.source.FrameID || stored.incarnation != identity.incarnation {
		return ErrExtractionSourceChanged
	}
	if stored.encoded == identity.encoded {
		return nil
	}
	if binding != extractionReceiptReadOnly || source.Transcript == nil || identity.source.Transcript == nil {
		return ErrExtractionSourceChanged
	}
	// A restarted runtime recaptures current branch coordinates for the same
	// immutable completion. Only this read path may recover the original result;
	// it must not grant permission to apply a differently bound mutation plan.
	proof := *source.Transcript
	proof.BranchID, proof.BranchGeneration = identity.source.Transcript.BranchID, identity.source.Transcript.BranchGeneration
	source.Transcript = &proof
	rebound, err := identifyExtractionSource(source)
	if err != nil || rebound.encoded != identity.encoded {
		return ErrExtractionSourceChanged
	}
	return nil
}

func (s *Store) insertExtractionReceiptTx(ctx context.Context, tx memoryTransaction, identity extractionSourceIdentity, inputDigest, planDigest string, result ExtractionCommitResult) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode memory extraction receipt: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_extraction_receipts(
		source_key,owner_user_id,project_id,frame_id,frame_incarnation_id,source_json,source_sha256,
		input_sha256,plan_sha256,result_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		identity.key, identity.source.OwnerUserID, identity.source.ProjectID, identity.source.FrameID, identity.incarnation,
		identity.encoded, identity.digest, inputDigest, planDigest, string(encoded), s.now().UTC())
	if err != nil {
		return fmt.Errorf("persist atomic memory extraction receipt: %w", err)
	}
	return nil
}
