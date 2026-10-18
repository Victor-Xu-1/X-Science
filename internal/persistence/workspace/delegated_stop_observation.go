package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	transcriptstore "x-science/internal/persistence/transcript"
)

// The original registration's own done channel is an observation, not a second
// terminal authority. Remember it even if independent executor persistence is
// still pending, so later readback can combine it with real executor settlement.
func (s *Store) RecordDelegatedStopObservation(ctx context.Context, owner string, observed DelegatedStopReceipt) (DelegatedStopReceipt, error) {
	var result DelegatedStopReceipt
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		scope, _, err := delegatedScopeTx(ctx, tx, owner, observed.Request.Scope.ParentFrameID)
		if err != nil {
			return err
		}
		record, raw, found, err := readDelegatedStopTx(ctx, tx, owner, scope, observed.Request.RequestID)
		if err != nil {
			return err
		}
		if !found || !reflect.DeepEqual(record.Receipt.Request, observed.Request) || len(record.Receipt.Targets) != len(observed.Targets) {
			return ErrDelegatedStopRequestConflict
		}
		before, _ := json.Marshal(record)
		for i, current := range record.Receipt.Targets {
			next := observed.Targets[i]
			if next.Target != current.Target {
				return ErrDelegatedStopRequestConflict
			}
			if next.ExecutionObservation == "stopped" {
				if record.RegistrationsStopped == nil {
					record.RegistrationsStopped = map[string]bool{}
				}
				record.RegistrationsStopped[current.Target.FrameID] = true
			}
		}
		if err := materializeDelegatedStopObservationTx(ctx, tx, &record); err != nil {
			return err
		}
		result = record.Receipt
		result.Replayed = observed.Replayed
		after, _ := json.Marshal(record)
		if string(before) == string(after) {
			return nil
		}
		id := delegatedRecordID("stop", owner, scope.ParentFrameIncarnationID, observed.Request.RequestID)
		if err := writeDelegatedRecord(ctx, tx, scope.ParentFrameID, id, DelegatedStopReceiptEventType, raw, record, s.now().UTC()); err != nil {
			return err
		}
		digest := sha256.Sum256(after)
		return s.enqueueDelegatedStopChangedTx(ctx, tx, owner, scope, id+":observed:"+hex.EncodeToString(digest[:]))
	})
	return result, err
}

func materializeDelegatedStopObservationTx(ctx context.Context, tx workspaceTransaction, record *delegatedStopStored) error {
	settled := true
	for i, outcome := range record.Receipt.Targets {
		if outcome.Outcome == "already_terminal" {
			continue
		}
		if !record.RegistrationsStopped[outcome.Target.FrameID] {
			settled = false
			continue
		}
		unconfirmed, err := delegatedKernelExecutionUnconfirmedTx(ctx, tx, record.OwnerID, record.Receipt.Request.Scope, outcome.Target)
		if err != nil {
			return err
		}
		if unconfirmed {
			settled = false
			record.Receipt.Targets[i].Diagnostic = "execution_settlement_pending"
			continue
		}
		record.Receipt.Targets[i].Outcome = "cancelled"
		record.Receipt.Targets[i].ExecutionObservation = "stopped"
		record.Receipt.Targets[i].Diagnostic = ""
	}
	if settled {
		record.Receipt.State = "settled"
	}
	return nil
}

func (s *Store) enqueueDelegatedStopChangedTx(ctx context.Context, tx workspaceTransaction, owner string, scope DelegatedStopScope, id string) error {
	actual, project, err := delegatedScopeTx(ctx, tx, owner, scope.ParentFrameID)
	if err != nil {
		return err
	}
	if actual != scope {
		return ErrDelegatedStopScopeStale
	}
	_, err = s.enqueueRealtimeOutboxTransaction(ctx, tx, RealtimeEventInput{ID: id, UserID: owner, ProjectID: project,
		RootFrameID: scope.RootFrameID, FrameID: scope.ParentFrameID, Type: "conversation.listChanged", Payload: map[string]any{
			"conversation_id": scope.ParentFrameID, "action": "updated", "source": "xscience", "delegated_stop_changed": true,
			"parent_frame_incarnation_id": scope.ParentFrameIncarnationID, "root_frame_incarnation_id": scope.RootFrameIncarnationID}}, "", "")
	return err
}
