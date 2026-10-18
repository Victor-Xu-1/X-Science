package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// FinishContextCompaction is the only maintenance boundary writer. A boundary
// and its real runner terminal receipt either both commit or neither commits.
func (r *Repository) FinishContextCompaction(ctx context.Context, claim RunnerClaim, request ContextCompactionRequest, boundary map[string]any) (ContextCompactionReceipt, error) {
	var result ContextCompactionReceipt
	if request.Validate() != nil || !validManualCompactionBoundary(boundary, request) {
		return result, ErrContextCompactionInvalid
	}
	err := r.withImmediate(ctx, func(conn *sql.Conn) error {
		stream, err := getStreamConn(ctx, conn, claim.StreamUID, claim.OwnerID)
		if err != nil {
			return err
		}
		result, err = contextCompactionReceiptConn(ctx, conn, claim.OwnerID, stream.FrameID, request, r.now().UTC())
		if err != nil {
			return err
		}
		if result.TerminalEventID > 0 {
			if result.State != "committed" || result.Attempt != claim.Attempt {
				return ErrClaimStale
			}
			if _, err := validateClaimConn(ctx, conn, claim, r.now().UTC(), false); err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}
		stream, err = validateClaimConn(ctx, conn, claim, r.now().UTC(), true)
		if err != nil {
			return err
		}
		if err = validateContextCompactionAttemptConn(ctx, conn, stream, claim.Attempt, claim.ClaimedInputRevision, request); err != nil {
			return err
		}
		target, _, _, err := contextCompactionTargetConn(ctx, conn, claim.OwnerID, stream.FrameID)
		if err != nil {
			return err
		}
		if target.BranchID != request.Expected.BranchID || target.BranchGeneration != request.Expected.BranchGeneration ||
			target.InputRevision != request.Expected.InputRevision+1 || claim.ClaimedInputRevision != target.InputRevision {
			return ErrContextCompactionStale
		}
		var queued bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM queued_user_messages WHERE frame_id=? AND state IN ('queued','delivering'))`, stream.FrameID).Scan(&queued); err != nil {
			return err
		}
		if queued {
			return ErrContextCompactionStale
		}
		if err = CheckFrameMessageIntentAdmission(ctx, conn, stream.FrameID, result.InputMessageID, func() *FrameMessageIntent {
			intent := request.MessageIntent(claim.OwnerID, stream.FrameID)
			return &intent
		}()); err != nil {
			return err
		}
		raw, err := json.Marshal(boundary)
		if err != nil {
			return err
		}
		_, _, _, err = appendRunnerCheckpointConn(ctx, conn, AppendRunnerCheckpointInput{Claim: claim, ClientMessageID: "context-compaction-boundary:" + request.RequestID,
			Phase: RunnerPhaseExecuting, Resumable: false, PayloadJSON: raw, Destinations: []string{"ws"}}, r.now().UTC())
		if err != nil {
			return err
		}
		_, _, _, err = r.finishRunnerConn(ctx, conn, FinishRunnerInput{contextCompactionBoundary: true, Claim: claim, ClientMessageID: "context-compaction-terminal:" + request.RequestID,
			Status: "completed", PayloadJSON: contextCompactionTerminalPayload(request, "completed"), Destinations: []string{"ws"}})
		if err != nil {
			return err
		}
		result, err = contextCompactionReceiptConn(ctx, conn, claim.OwnerID, stream.FrameID, request, r.now().UTC())
		return err
	})
	return result, schemaError(err)
}

func contextCompactionRequestForClaimConn(ctx context.Context, conn *sql.Conn, stream Stream, claim RunnerClaim) (*ContextCompactionRequest, error) {
	if stream.Kind != StreamKindFrameRef {
		return nil, nil
	}
	installed, err := contextCompactionLedgerForStream(ctx, conn, stream.UID)
	if err != nil || !installed {
		return nil, err
	}
	input, err := readAdmittedRunnerInputContextConn(ctx, conn, stream, claim.ClaimedInputRevision, true)
	if err != nil || input == nil {
		return nil, err
	}
	request, err := ContextCompactionRequestFromPayload(map[string]any{"inputData": input.InputData})
	if err != nil || request == nil {
		return request, err
	}
	if input.ClientMessageID != request.MessageIntent(stream.OwnerID, stream.FrameID).InputMessageID {
		return nil, ErrContextCompactionConflict
	}
	if err := validateContextCompactionAttemptConn(ctx, conn, stream, claim.Attempt, claim.ClaimedInputRevision, *request); err != nil {
		return nil, err
	}
	return request, nil
}

func validManualCompactionBoundary(payload map[string]any, request ContextCompactionRequest) bool {
	if payload["toolPhase"] != ContextCompactionPhase || payload["status"] != "completed" || payload["purpose"] != ContextCompactionPurpose || !IsCompactionBoundaryPayload(payload) {
		return false
	}
	raw, err := json.Marshal(payload["maintenance"])
	var stored ContextCompactionRequest
	if err != nil || json.Unmarshal(raw, &stored) != nil || stored != request {
		return false
	}
	value, _ := payload["compaction"].(map[string]any)
	return value["request_id"] == request.RequestID && value["reason"] == "manual"
}

func requireContextCompactionBoundaryConn(ctx context.Context, conn *sql.Conn, stream Stream, claim RunnerClaim, request ContextCompactionRequest) error {
	event, found, err := findEventByClientID(ctx, conn, stream.UID, "context-compaction-boundary:"+request.RequestID)
	if err != nil {
		return err
	}
	if !found || event.RunnerAttempt == nil || *event.RunnerAttempt != claim.Attempt {
		return ErrEventConflict
	}
	var payload map[string]any
	if json.Unmarshal(event.PayloadJSON, &payload) != nil || !validManualCompactionBoundary(payload, request) {
		return ErrEventConflict
	}
	return nil
}

func normalizedMaintenanceTerminalPayload(original []byte, request ContextCompactionRequest) ([]byte, error) {
	var value map[string]any
	if json.Unmarshal(original, &value) != nil {
		return nil, ErrEventConflict
	}
	value["purpose"], value["maintenance"] = ContextCompactionPurpose, request
	delete(value, "assistant_segment")
	return json.Marshal(value)
}

func (r *Repository) FailContextCompaction(ctx context.Context, claim RunnerClaim, request ContextCompactionRequest) (ContextCompactionReceipt, error) {
	stream, err := r.GetStream(ctx, claim.StreamUID, claim.OwnerID)
	if err != nil {
		return ContextCompactionReceipt{}, err
	}
	_, _, _, err = r.FinishRunner(ctx, FinishRunnerInput{Claim: claim, ClientMessageID: "context-compaction-failed:" + request.RequestID,
		Status: "failed", PayloadJSON: contextCompactionTerminalPayload(request, "failed"), Destinations: []string{"ws"}})
	if err != nil && !errors.Is(err, ErrClaimStale) {
		return ContextCompactionReceipt{}, err
	}
	return r.ReadContextCompaction(ctx, claim.OwnerID, stream.FrameID, request)
}
