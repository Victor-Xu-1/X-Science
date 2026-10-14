package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"x-science/internal/kernelcontract"
	transcriptstore "x-science/internal/persistence/transcript"
)

// KernelBatchAdmissionVersion separates the immutable ordered proposal from
// the executable input resolved after preceding tool receipts have committed.
const KernelBatchAdmissionVersion = transcriptstore.KernelBatchAdmissionVersion

type KernelBatchAdmission = transcriptstore.RunnerKernelBatchAdmission

type kernelBatchStartAdmission struct {
	Ordinal   int64
	Arguments json.RawMessage
}

type KernelBatchAdmissionState struct {
	Deferred       bool
	Item           ToolCallBatchItem
	Operation      KernelLocalOperation
	OperationFound bool
}

func loadKernelBatchAdmissionSource(ctx context.Context, tx *transcriptstore.ImmediateTransaction, batch ToolCallBatch) (transcriptstore.Event, bool, error) {
	event := transcriptstore.Event{StreamUID: batch.StreamUID, EventID: batch.SourceEventID}
	var attempt int64
	err := tx.QueryRowContext(ctx, `SELECT publication_seq,client_message_id,runner_attempt,payload_json
		FROM transcript_events WHERE stream_uid=? AND event_id=?`, batch.StreamUID, batch.SourceEventID).Scan(
		&event.PublicationSeq, &event.ClientMessageID, &attempt, &event.PayloadJSON)
	if err != nil {
		return event, false, err
	}
	event.RunnerAttempt = &attempt
	var source struct {
		Version int `json:"kernelAdmissionVersion"`
	}
	if json.Unmarshal(event.PayloadJSON, &source) != nil || (source.Version != 0 && source.Version != KernelBatchAdmissionVersion) {
		return event, false, ErrKernelLocalOperationConflict
	}
	return event, source.Version == KernelBatchAdmissionVersion, nil
}

// KernelToolCallBatchAdmission reads only the exact current item owned by the
// live claim. Missing operations are legal only before a marked item's start.
func (s *Store) KernelToolCallBatchAdmission(ctx context.Context, claim transcriptstore.RunnerClaim, batchID string, ordinal int64) (KernelBatchAdmissionState, error) {
	var result KernelBatchAdmissionState
	repo, err := s.TranscriptRepository(ctx)
	if err != nil {
		return result, err
	}
	err = repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		if _, err := tx.ValidateLiveRunnerClaim(ctx, claim); err != nil {
			return err
		}
		batch, found, err := getToolCallBatchQuery(ctx, tx, batchID)
		if err != nil {
			return err
		}
		if !found || batch.NextOrdinal != ordinal {
			return ErrToolCallBatchConflict
		}
		if err := validateToolCallBatchAuthority(ctx, tx, batch, claim); err != nil {
			return err
		}
		item, found, err := getToolCallBatchItemQuery(ctx, tx, batchID, ordinal)
		if err != nil {
			return err
		}
		if !found {
			return ErrToolCallBatchConflict
		}
		_, deferred, err := loadKernelBatchAdmissionSource(ctx, tx, batch)
		if err != nil {
			return err
		}
		result.Deferred, result.Item = deferred, item
		if !deferred {
			return nil
		}
		if !kernelcontract.Admitted(item.ToolName, item.ArgumentsJSON) {
			return nil // Ordinary invalid proposals have no executable ledger row.
		}
		operation, found, err := getKernelLocalOperationBySourceQuery(ctx, tx, batch.StreamUID, batch.SourceEventID, ordinal)
		if err != nil {
			return err
		}
		if !found {
			if item.State != ToolCallBatchItemStatePending || item.StartedEventID != 0 {
				return ErrKernelLocalOperationConflict
			}
			return nil
		}
		if operation.OwnerUserID != claim.OwnerID || operation.ToolCallID != item.ToolCallID || operation.Tool != item.ToolName || item.StartedEventID <= 0 {
			return ErrKernelLocalOperationConflict
		}
		var payload []byte
		if err := tx.QueryRowContext(ctx, `SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, batch.StreamUID, item.StartedEventID).Scan(&payload); err != nil {
			return err
		}
		var witness struct {
			Admission KernelBatchAdmission `json:"kernelAdmission"`
		}
		if json.Unmarshal(payload, &witness) != nil || !kernelBatchAdmissionMatches(witness.Admission, batch, item) || !kernelBatchAdmissionInputMatches(witness.Admission, operation.InputJSON) {
			return ErrKernelLocalOperationConflict
		}
		result.Operation, result.OperationFound = operation, true
		return nil
	})
	return result, err
}

func kernelBatchAdmissionMatches(admission KernelBatchAdmission, batch ToolCallBatch, item ToolCallBatchItem) bool {
	return admission.Version == KernelBatchAdmissionVersion && admission.BatchID == batch.BatchID &&
		admission.Ordinal == item.Ordinal && admission.ProposalSHA256 == item.ArgumentsSHA256
}

func kernelBatchAdmissionInputMatches(admission KernelBatchAdmission, input []byte) bool {
	digest := sha256.Sum256(input)
	return admission.InputByteLength == len(input) && admission.InputSHA256 == hex.EncodeToString(digest[:])
}

// CreateKernelLocalOperationForBatchStartTx is called after the ordinary start
// mutation in the same transaction. The source coordinates remain the original
// proposal's coordinates; StartedEventID binds its one canonical-input witness.
func (s *Store) CreateKernelLocalOperationForBatchStartTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, claim transcriptstore.RunnerClaim, event transcriptstore.Event, admittedInput json.RawMessage) (KernelLocalOperation, error) {
	if _, err := tx.ValidateLiveRunnerClaim(ctx, claim); err != nil {
		return KernelLocalOperation{}, err
	}
	var payload struct {
		ToolCallID string               `json:"toolCallId"`
		ToolName   string               `json:"toolName"`
		Admission  KernelBatchAdmission `json:"kernelAdmission"`
	}
	if json.Unmarshal(event.PayloadJSON, &payload) != nil {
		return KernelLocalOperation{}, ErrKernelLocalOperationConflict
	}
	a := payload.Admission
	batch, found, err := getToolCallBatchQuery(ctx, tx, a.BatchID)
	if err != nil {
		return KernelLocalOperation{}, err
	}
	if !found || batch.State != ToolCallBatchStateRunning || batch.NextOrdinal != a.Ordinal {
		return KernelLocalOperation{}, ErrKernelLocalOperationConflict
	}
	if err := validateToolCallBatchAuthority(ctx, tx, batch, claim); err != nil {
		return KernelLocalOperation{}, err
	}
	item, found, err := getToolCallBatchItemQuery(ctx, tx, batch.BatchID, a.Ordinal)
	if err != nil {
		return KernelLocalOperation{}, err
	}
	if !found || item.State != ToolCallBatchItemStateRunning || item.StartedEventID != event.EventID || item.ToolCallID != payload.ToolCallID || item.ToolName != payload.ToolName || !kernelBatchAdmissionMatches(a, batch, item) {
		return KernelLocalOperation{}, ErrKernelLocalOperationConflict
	}
	if _, _, _, err := validateToolCallBatchCheckpointEvent(ctx, tx, batch, item, claim, event, "start", false); err != nil {
		return KernelLocalOperation{}, err
	}
	canonical, _, err := kernelcontract.CanonicalInput(item.ToolName, admittedInput)
	if err != nil || !bytes.Equal(canonical, admittedInput) || !kernelBatchAdmissionInputMatches(a, canonical) {
		return KernelLocalOperation{}, ErrKernelLocalOperationConflict
	}
	source, deferred, err := loadKernelBatchAdmissionSource(ctx, tx, batch)
	if err != nil {
		return KernelLocalOperation{}, err
	}
	if !deferred {
		return KernelLocalOperation{}, ErrKernelLocalOperationConflict
	}
	operations, err := s.createKernelLocalOperationsForCheckpointTx(ctx, tx, source, &kernelBatchStartAdmission{Ordinal: item.Ordinal, Arguments: canonical})
	if err != nil {
		return KernelLocalOperation{}, err
	}
	if len(operations) != 1 || operations[0].ToolCallID != item.ToolCallID {
		return KernelLocalOperation{}, ErrKernelLocalOperationConflict
	}
	return operations[0], nil
}
