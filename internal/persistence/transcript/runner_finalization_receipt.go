package transcript

import (
	"context"
	"database/sql"
	"reflect"
	"time"
)

// RunnerFinalizationRead exposes queries only to the same-snapshot Workspace
// effect inspector. It has no append, finish, enqueue, or Exec operation.
type RunnerFinalizationRead struct{ conn *sql.Conn }

func (read RunnerFinalizationRead) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return read.conn.QueryRowContext(ctx, query, args...)
}

// InspectRunnerFinalization never fills partial old records. A matching receipt
// pair is replayed read-only; an orphan assistant is an explicit conflict.
func (tx *ImmediateTransaction) InspectRunnerFinalization(ctx context.Context, input RunnerFinalizationInput, expected *RunnerFinalizationReceipt) (RunnerFinalizationReceipt, bool, error) {
	if tx == nil || tx.repository == nil || tx.conn == nil {
		return RunnerFinalizationReceipt{}, false, ErrSchemaUnavailable
	}
	return inspectRunnerFinalizationConn(ctx, tx.conn, input, expected, tx.repository.now().UTC())
}

func (r *Repository) ReadRunnerFinalization(ctx context.Context, input RunnerFinalizationInput, expected *RunnerFinalizationReceipt,
	inspect func(context.Context, RunnerFinalizationRead, RunnerFinalizationReceipt) error,
) (RunnerFinalizationReceipt, bool, error) {
	if r == nil || r.readDatabase() == nil {
		return RunnerFinalizationReceipt{}, false, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return RunnerFinalizationReceipt{}, false, schemaError(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return RunnerFinalizationReceipt{}, false, schemaError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(cleanup, "ROLLBACK")
	}()
	receipt, found, err := inspectRunnerFinalizationConn(ctx, conn, input, expected, r.now().UTC())
	if err != nil || !found {
		return receipt, found, schemaError(err)
	}
	if inspect != nil {
		if err := inspect(ctx, RunnerFinalizationRead{conn: conn}, receipt); err != nil {
			return RunnerFinalizationReceipt{}, false, err
		}
	}
	return receipt, true, nil
}

func inspectRunnerFinalizationConn(ctx context.Context, conn *sql.Conn, input RunnerFinalizationInput, expected *RunnerFinalizationReceipt, now time.Time) (RunnerFinalizationReceipt, bool, error) {
	if err := ValidateRunnerFinalizationInput(input); err != nil {
		return RunnerFinalizationReceipt{}, false, err
	}
	claim := input.Finish.Claim
	stream, err := validateClaimConn(ctx, conn, claim, now, false)
	if err != nil {
		return RunnerFinalizationReceipt{}, false, err
	}
	receipt := RunnerFinalizationReceipt{Stream: stream}
	if err := conn.QueryRowContext(ctx, `SELECT active_branch_id,generation FROM transcript_branch_state WHERE stream_uid=?`, stream.UID).Scan(&receipt.BranchID, &receipt.BranchGeneration); err != nil {
		return receipt, false, err
	}
	terminal, found, err := findEventByClientID(ctx, conn, stream.UID, input.Finish.ClientMessageID)
	if err != nil {
		return receipt, false, err
	}
	if !found {
		if input.Assistant != nil {
			_, exists, err := findEventByClientID(ctx, conn, stream.UID, input.Assistant.ClientMessageID)
			if err != nil {
				return receipt, false, err
			}
			if exists {
				return receipt, false, ErrEventConflict
			}
		}
		return receipt, false, nil
	}
	input.Finish.Destinations = terminalDeliveryDestinations(stream, input.Finish.Destinations)
	maintenance, err := contextCompactionRequestForClaimConn(ctx, conn, stream, claim)
	if err != nil {
		return receipt, false, err
	}
	if maintenance != nil {
		input.Finish.PayloadJSON, err = normalizedMaintenanceTerminalPayload(input.Finish.PayloadJSON, *maintenance)
		if err != nil {
			return receipt, false, err
		}
	}
	if terminal.Source != EventSourcePayload || terminal.FrameEventID != nil || terminal.RunnerAttempt == nil || *terminal.RunnerAttempt != claim.Attempt {
		return receipt, false, ErrEventConflict
	}
	if err := validateFinalizationBranchMember(ctx, conn, stream.UID, receipt.BranchID, terminal.EventID); err != nil {
		return receipt, false, err
	}
	if err := loadMatchingReceipt(ctx, conn, input.Finish, terminal, &receipt.Finish); err != nil {
		return receipt, false, err
	}
	receipt.Terminal = terminal
	if input.Assistant != nil {
		assistant := *input.Assistant
		if !sameFinalizationClaim(assistant.Claim, claim) || assistant.Type != "assistant_message" {
			return receipt, false, ErrEventConflict
		}
		if err := validateRunnerEventInput(assistant); err != nil {
			return receipt, false, err
		}
		event, exists, err := findEventByClientID(ctx, conn, stream.UID, assistant.ClientMessageID)
		if err != nil {
			return receipt, false, err
		}
		if !exists || !eventMatches(event, eventRecord{clientMessageID: assistant.ClientMessageID, eventType: assistant.Type, source: assistant.Source,
			runnerAttempt: &claim.Attempt, payloadJSON: assistant.PayloadJSON, frameEventID: assistant.FrameEventID, destinations: assistant.Destinations}) || event.PublicationSeq >= terminal.PublicationSeq {
			return receipt, false, ErrEventConflict
		}
		destinations, err := deliveryDestinations(ctx, conn, stream.UID, event.PublicationSeq)
		if err != nil {
			return receipt, false, err
		}
		if !reflect.DeepEqual(destinations, normalizedDestinations(assistant.Destinations)) {
			return receipt, false, ErrEventConflict
		}
		if err := validateFinalizationBranchMember(ctx, conn, stream.UID, receipt.BranchID, event.EventID); err != nil {
			return receipt, false, err
		}
		receipt.Assistant = event
		receipt.AssistantReferences, err = listArtifactReferencesForEventConn(ctx, conn, stream.UID, claim.Attempt, event.EventID)
		if err != nil {
			return receipt, false, err
		}
	}
	receipt.TerminalReferences, err = listArtifactReferencesForEventConn(ctx, conn, stream.UID, claim.Attempt, terminal.EventID)
	if err != nil {
		return receipt, false, err
	}
	receipt.ArtifactBindings, err = finalizationArtifactBindingsConn(ctx, conn, stream.UID, claim.Attempt, receipt.Assistant.EventID, terminal.EventID)
	if err != nil {
		return receipt, false, err
	}
	if expected != nil && !sameRunnerFinalizationReceipt(receipt, *expected) {
		return receipt, false, ErrEventConflict
	}
	return receipt, true, nil
}

func sameFinalizationClaim(left, right RunnerClaim) bool {
	return left.StreamUID == right.StreamUID && left.OwnerID == right.OwnerID && left.RunnerID == right.RunnerID && left.Attempt == right.Attempt &&
		left.ClaimToken == right.ClaimToken && left.ClaimedInputRevision == right.ClaimedInputRevision && left.ResumeSource == right.ResumeSource && left.ResumeCheckpoint == right.ResumeCheckpoint
}

func validateFinalizationBranchMember(ctx context.Context, conn *sql.Conn, streamUID, branchID string, eventID int64) error {
	var member bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_branch_events WHERE stream_uid=? AND branch_id=? AND event_id=?)`, streamUID, branchID, eventID).Scan(&member); err != nil {
		return err
	}
	if !member {
		return ErrBranchStateStale
	}
	return nil
}

func finalizationArtifactBindingsConn(ctx context.Context, conn *sql.Conn, uid string, attempt, assistantID, terminalID int64) ([]RunnerFinalizationArtifactBinding, error) {
	rows, err := conn.QueryContext(ctx, `SELECT source_event_id,ordinal,artifact_id,version_id,relation,bound_event_id
		FROM transcript_artifact_commits WHERE stream_uid=? AND runner_attempt=? AND bound_event_id IN (?,?)
		ORDER BY source_event_id,ordinal,artifact_id,version_id,relation,bound_event_id`, uid, attempt, assistantID, terminalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []RunnerFinalizationArtifactBinding{}
	for rows.Next() {
		var value RunnerFinalizationArtifactBinding
		if err := rows.Scan(&value.SourceEventID, &value.Ordinal, &value.ArtifactID, &value.VersionID, &value.Relation, &value.BoundEventID); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func sameRunnerFinalizationReceipt(left, right RunnerFinalizationReceipt) bool {
	if left.Stream.UID != right.Stream.UID || left.Stream.OwnerID != right.Stream.OwnerID || left.Stream.Epoch != right.Stream.Epoch ||
		left.BranchID != right.BranchID || left.BranchGeneration != right.BranchGeneration ||
		!sameFinalizationEvent(left.Assistant, right.Assistant) || !sameFinalizationEvent(left.Terminal, right.Terminal) ||
		left.Finish.StreamUID != right.Finish.StreamUID || left.Finish.Attempt != right.Finish.Attempt || left.Finish.EventID != right.Finish.EventID || left.Finish.Status != right.Finish.Status || !left.Finish.FinishedAt.Equal(right.Finish.FinishedAt) {
		return false
	}
	return sameFinalizationReferences(left.AssistantReferences, right.AssistantReferences) && sameFinalizationReferences(left.TerminalReferences, right.TerminalReferences) && reflect.DeepEqual(left.ArtifactBindings, right.ArtifactBindings)
}

func sameFinalizationEvent(left, right Event) bool {
	if left.EventID == 0 || right.EventID == 0 {
		return left.EventID == right.EventID
	}
	return left.StreamUID == right.StreamUID && left.EventID == right.EventID && left.PublicationSeq == right.PublicationSeq && left.ClientMessageID == right.ClientMessageID &&
		left.Type == right.Type && left.Source == right.Source && reflect.DeepEqual(left.RunnerAttempt, right.RunnerAttempt) && reflect.DeepEqual(left.FrameEventID, right.FrameEventID) && jsonEqual(left.PayloadJSON, right.PayloadJSON) && left.CreatedAt.Equal(right.CreatedAt)
}

func sameFinalizationReferences(left, right []ArtifactReference) bool {
	if len(left) != len(right) {
		return false
	}
	for index, value := range left {
		expected := right[index]
		if value.StreamUID != expected.StreamUID || value.RunnerAttempt != expected.RunnerAttempt || value.SourceEventID != expected.SourceEventID || value.Ordinal != expected.Ordinal ||
			value.ArtifactID != expected.ArtifactID || value.VersionID != expected.VersionID || value.Relation != expected.Relation || !value.CreatedAt.Equal(expected.CreatedAt) {
			return false
		}
	}
	return true
}
