package workspace

import (
	"context"
	"reflect"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

type delegatedStopStored struct {
	Version              int                  `json:"version"`
	OwnerID              string               `json:"owner_id"`
	HostCallID           string               `json:"host_call_id,omitempty"`
	HostReason           string               `json:"host_reason,omitempty"`
	RegistrationsStopped map[string]bool      `json:"registrations_stopped,omitempty"`
	Receipt              DelegatedStopReceipt `json:"receipt"`
}

func ValidateDelegatedStopRequest(request DelegatedStopRequest) error {
	if request.Schema != "x-science.delegated-stop.v1" || request.Scope.ParentFrameID == "" || request.Scope.RootFrameID == "" ||
		request.Scope.ParentFrameIncarnationID == "" || request.Scope.RootFrameIncarnationID == "" || !transcriptstore.IsValidBranchID(request.Branch.BranchID) ||
		request.Branch.Generation <= 0 || request.ParentInputRevision < 0 || request.ExpectedFenceGeneration < 0 {
		return ErrDelegatedStopInvalid
	}
	if _, err := uuid.Parse(request.RequestID); err != nil {
		return ErrDelegatedStopInvalid
	}
	if request.Mode == "branch" && request.Target == nil {
		return nil
	}
	if request.Mode != "child" || request.Target == nil {
		return ErrDelegatedStopInvalid
	}
	target := request.Target
	if target.FrameID == "" || target.FrameIncarnationID == "" || target.StreamUID == "" || target.StreamEpoch <= 0 || target.InputMessageID == "" || target.InputRevision <= 0 || target.RunnerAttempt < 0 {
		return ErrDelegatedStopInvalid
	}
	return nil
}

func (s *Store) ReadDelegatedStop(ctx context.Context, owner, parent, requestID string) (DelegatedStopReceipt, bool, error) {
	if _, err := uuid.Parse(requestID); err != nil {
		return DelegatedStopReceipt{}, false, ErrDelegatedStopInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DelegatedStopReceipt{}, false, err
	}
	defer tx.Rollback()
	scope, _, err := delegatedScopeTx(ctx, tx, owner, parent)
	if err != nil {
		return DelegatedStopReceipt{}, false, err
	}
	record, _, found, err := readDelegatedStopTx(ctx, tx, owner, scope, requestID)
	if err != nil {
		return DelegatedStopReceipt{}, false, err
	}
	if found {
		if err := materializeDelegatedStopObservationTx(ctx, tx, &record); err != nil {
			return DelegatedStopReceipt{}, false, err
		}
	}
	return record.Receipt, found, tx.Commit()
}

func readDelegatedStopTx(ctx context.Context, tx workspaceTransaction, owner string, scope DelegatedStopScope, requestID string) (delegatedStopStored, string, bool, error) {
	var record delegatedStopStored
	id := delegatedRecordID("stop", owner, scope.ParentFrameIncarnationID, requestID)
	raw, found, err := readDelegatedRecord(ctx, tx, scope.ParentFrameID, id, DelegatedStopReceiptEventType, &record)
	if err == nil && found && (record.Version != 1 || record.OwnerID != owner || record.Receipt.Request.Scope != scope || record.Receipt.Request.RequestID != requestID || !validStoredDelegatedStopRequest(record)) {
		err = ErrDelegatedStopScopeStale
	}
	return record, raw, found, err
}

func validStoredDelegatedStopRequest(record delegatedStopStored) bool {
	if record.HostCallID == "" {
		return ValidateDelegatedStopRequest(record.Receipt.Request) == nil
	}
	r := record.Receipt.Request
	return r.Schema == "x-science.delegated-host-stop.v1" && r.Mode == "child" && r.Target != nil &&
		r.Scope.ParentFrameIncarnationID != "" && r.Scope.RootFrameIncarnationID != "" && r.Target.FrameIncarnationID != "" &&
		r.Target.StreamUID != "" && r.Target.StreamEpoch > 0 && r.Target.InputRevision > 0 && r.Target.InputMessageID != "" && r.Target.RunnerAttempt >= 0
}

// No physical stop signal is emitted here. Callers signal only captured exact
// registrations after this transaction has successfully committed.
func (s *Store) AdmitDelegatedStop(ctx context.Context, owner string, request DelegatedStopRequest) (DelegatedStopReceipt, error) {
	if err := ValidateDelegatedStopRequest(request); err != nil {
		return DelegatedStopReceipt{}, err
	}
	var result DelegatedStopReceipt
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		scope, _, err := delegatedScopeTx(ctx, tx, owner, request.Scope.ParentFrameID)
		if err != nil {
			return err
		}
		if scope != request.Scope {
			return ErrDelegatedStopScopeStale
		}
		existing, _, found, err := readDelegatedStopTx(ctx, tx, owner, scope, request.RequestID)
		if err != nil {
			return err
		}
		if found {
			if !reflect.DeepEqual(existing.Receipt.Request, request) {
				return ErrDelegatedStopRequestConflict
			}
			result = existing.Receipt
			result.Replayed = true
			return nil
		}
		snapshot, err := delegatedStopSnapshotTx(ctx, tx, owner, scope.ParentFrameID, request.Branch.BranchID)
		if err != nil {
			return err
		}
		if snapshot.Branch != request.Branch {
			return ErrDelegatedStopBranchStale
		}
		if snapshot.ParentInputRevision != request.ParentInputRevision || snapshot.FenceGeneration != request.ExpectedFenceGeneration {
			return ErrDelegatedStopTargetStale
		}
		selected := []DelegatedStopTarget{}
		for _, item := range snapshot.Targets {
			if request.Mode == "child" && item.FrameID == request.Target.FrameID {
				if !item.CanStop && item.Status != "terminal" {
					return ErrDelegatedStopOriginUnavailable
				}
				if item.Target == nil || *item.Target != *request.Target {
					return ErrDelegatedStopTargetStale
				}
				selected = append(selected, *item.Target)
			} else if request.Mode == "branch" && item.InSelectedBranch && item.CanStop && item.Target != nil {
				selected = append(selected, *item.Target)
			}
		}
		if request.Mode == "child" && len(selected) != 1 {
			return ErrDelegatedStopUnavailable
		}
		if request.Mode == "branch" {
			uid, epoch, _, err := delegatedStreamTx(ctx, tx, owner, scope.ParentFrameID)
			if err != nil {
				return err
			}
			fence, id, raw, err := delegationFenceTx(ctx, tx, owner, scope, uid, epoch, request.Branch.BranchID)
			if err != nil {
				return err
			}
			fence.Generation++
			if err := writeDelegatedRecord(ctx, tx, scope.ParentFrameID, id, delegationFenceEventType, raw, fence, s.now().UTC()); err != nil {
				return err
			}
		}
		result = DelegatedStopReceipt{Schema: "x-science.delegated-stop-receipt.v1", Request: request, AcceptedAt: s.now().UTC(), State: "stopping", Targets: []DelegatedStopOutcome{}}
		seen := map[string]bool{}
		for _, target := range selected {
			outcomes, err := s.cancelDelegatedSelectionTx(ctx, tx, owner, scope, target, seen)
			if err != nil {
				return err
			}
			result.Targets = append(result.Targets, outcomes...)
		}
		if len(result.Targets) == 0 {
			result.State = "settled"
		}
		id := delegatedRecordID("stop", owner, scope.ParentFrameIncarnationID, request.RequestID)
		if err := writeDelegatedRecord(ctx, tx, scope.ParentFrameID, id, DelegatedStopReceiptEventType, "", delegatedStopStored{Version: 1, OwnerID: owner, Receipt: result}, s.now().UTC()); err != nil {
			return err
		}
		return s.enqueueDelegatedStopChangedTx(ctx, tx, owner, scope, id+":changed")
	})
	return result, err
}

func (s *Store) cancelDelegatedTargetTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, owner string, scope DelegatedStopScope, expected DelegatedStopTarget) (DelegatedStopOutcome, error) {
	result := DelegatedStopOutcome{Target: expected, Outcome: "already_terminal", ExecutionObservation: "unavailable"}
	actual, status, err := delegatedStopTargetTx(ctx, tx, owner, scope, expected.FrameID)
	if err != nil {
		return result, err
	}
	if actual != expected {
		return result, ErrDelegatedStopTargetStale
	}
	if status == "terminal" {
		return result, nil
	}
	child, found, err := readDelegatedChildTx(ctx, tx, scope.ParentFrameID, expected.FrameID, owner)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrDelegatedStopUnavailable
	}
	cancelled, err := tx.CancelExactFrameRunner(ctx, owner, expected.FrameID, expected.StreamUID, expected.StreamEpoch, expected.InputRevision, expected.RunnerAttempt, "user_cancelled", []string{"ws"})
	if err != nil {
		return result, err
	}
	if cancelled.CurrentStatus != "cancelled" {
		return result, nil
	}
	if err := requestDetachedDelegatedCancellationTx(ctx, tx, owner, scope, expected, s.now().UTC()); err != nil {
		return result, err
	}
	if _, _, err := s.projectCancelledKernelChildTx(ctx, tx, child, "Stopped by its parent", cancelled.Receipt); err != nil {
		return result, err
	}
	result.Outcome = "cancel_requested"
	return result, nil
}
