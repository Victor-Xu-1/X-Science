package transcript

import (
	"context"
	"database/sql"
	"time"

	"x-science/internal/sessionagent"
)

func admitSessionAgentInputConn(ctx context.Context, conn *sql.Conn, stream Stream, clientID, messageOrigin string, data map[string]any, now time.Time) error {
	reference, err := sessionagent.InputReferenceFrom(data)
	if err != nil {
		return err
	}
	if stream.Kind != StreamKindFrameRef {
		if reference != nil {
			return sessionagent.ErrInvalid
		}
		return nil
	}
	if messageOrigin != "task_intent" || HiddenConversationControlIntent(map[string]any{"inputData": data}) != "" {
		if reference != nil {
			return sessionagent.ErrInvalid
		}
		return nil
	}
	branch, err := sessionagent.ActiveBranch(ctx, conn, stream.OwnerID, stream.FrameID)
	if err != nil {
		return err
	}
	return sessionagent.Admit(ctx, conn, stream.OwnerID, stream.FrameID, clientID, branch, reference, now)
}

// Common direct/coarse claim owner. Only a new ordinary Fresh input may bind
// a new definition. Recovery and approval continuation copy the previous
// immutable claim binding, even if newer inputs or desired revisions exist.
func bindSessionAgentClaimConn(ctx context.Context, conn *sql.Conn, stream Stream, input ClaimRunnerInput, attempt, previousAttempt, previousRevision, claimedRevision int64, now time.Time) error {
	if stream.Kind != StreamKindFrameRef {
		return nil
	}
	established, err := sessionagent.Established(ctx, conn, stream.FrameID)
	if err != nil || !established {
		return err
	}
	scope, err := sessionagent.ResolveScope(ctx, conn, stream.OwnerID, stream.FrameID)
	if err != nil {
		return err
	}
	var binding *sessionagent.Binding
	if previousAttempt > 0 && (input.ResumeSource != ResumeSourceFresh || claimedRevision <= previousRevision) {
		binding, err = sessionagent.ReadClaim(ctx, conn, stream.OwnerID, scope, stream.UID, stream.Epoch, previousAttempt)
	} else {
		ordinary, readErr := readAdmittedRunnerInputContextConn(ctx, conn, stream, claimedRevision, false)
		if readErr != nil {
			return readErr
		}
		if ordinary != nil {
			binding, err = sessionagent.ReadInput(ctx, conn, stream.OwnerID, scope, ordinary.ClientMessageID)
		}
	}
	if err != nil {
		return err
	}
	return sessionagent.BindClaim(ctx, conn, stream.OwnerID, scope, stream.UID, stream.Epoch, attempt, binding, now)
}

func (r *Repository) ClaimedRunnerAgentSnapshot(ctx context.Context, claim RunnerClaim) (*sessionagent.Snapshot, error) {
	return r.ClaimedRunnerAgentSnapshotForFrame(ctx, claim, "")
}

// Frame-bound consumers cannot borrow an otherwise valid claim from a
// different conversation. This remains a read projection of the same owner.
func (r *Repository) ClaimedRunnerAgentSnapshotForFrame(ctx context.Context, claim RunnerClaim, frameID string) (*sessionagent.Snapshot, error) {
	var result *sessionagent.Snapshot
	err := r.RunImmediate(ctx, func(tx *ImmediateTransaction) error {
		stream, err := validateClaimConn(ctx, tx.conn, claim, r.now().UTC(), true)
		if err != nil {
			return err
		}
		if frameID != "" && stream.FrameID != frameID {
			return ErrOwnerMismatch
		}
		binding, err := claimedAgentBindingConn(ctx, tx.conn, stream, claim)
		if err != nil || binding == nil {
			return err
		}
		definition, err := sessionagent.ReadSnapshot(ctx, tx, claim.OwnerID, binding.Scope, binding.Selection.SnapshotID)
		if err != nil {
			return err
		}
		result = &definition
		return nil
	})
	return result, err
}

func claimedAgentBindingConn(ctx context.Context, conn *sql.Conn, stream Stream, claim RunnerClaim) (*sessionagent.Binding, error) {
	if stream.Kind != StreamKindFrameRef {
		return nil, nil
	}
	established, err := sessionagent.Established(ctx, conn, stream.FrameID)
	if err != nil || !established {
		return nil, err
	}
	scope, err := sessionagent.ResolveScope(ctx, conn, claim.OwnerID, stream.FrameID)
	if err != nil {
		return nil, err
	}
	return sessionagent.ReadClaim(ctx, conn, claim.OwnerID, scope, stream.UID, stream.Epoch, claim.Attempt)
}

func (r *Repository) RecordClaimedRunnerAgentPreparation(ctx context.Context, claim RunnerClaim, snapshotID string, applied bool, notification func(*ImmediateTransaction, sessionagent.Binding) error) error {
	return r.RunImmediate(ctx, func(tx *ImmediateTransaction) error {
		stream, err := validateClaimConn(ctx, tx.conn, claim, r.now().UTC(), true)
		if err != nil {
			return err
		}
		binding, err := claimedAgentBindingConn(ctx, tx.conn, stream, claim)
		if err != nil {
			return err
		}
		if binding == nil || binding.Selection.SnapshotID != snapshotID {
			return sessionagent.ErrScopeStale
		}
		if applied {
			err = sessionagent.Applied(ctx, tx, claim.OwnerID, *binding, r.now().UTC())
		} else {
			err = sessionagent.PreparationFailed(ctx, tx, claim.OwnerID, *binding, r.now().UTC())
		}
		if err != nil || notification == nil {
			return err
		}
		return notification(tx, *binding)
	})
}
