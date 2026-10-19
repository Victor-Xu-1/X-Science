package workspace

import (
	"context"
	"database/sql"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Store) SessionDelegationPolicy(ctx context.Context, owner, frameID string) (SessionDelegationSnapshot, error) {
	if s == nil || s.readDatabase() == nil || ctx == nil {
		return SessionDelegationSnapshot{}, ErrSessionDelegationUnavailable
	}
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(frameID) == "" {
		return SessionDelegationSnapshot{}, ErrSessionDelegationInvalid
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SessionDelegationSnapshot{}, err
	}
	defer tx.Rollback()
	scope, _, err := sessionDelegationScopeTx(ctx, tx, owner, frameID)
	if err != nil {
		return SessionDelegationSnapshot{}, err
	}
	record, _, err := sessionDelegationPolicyRecordTx(ctx, tx, owner, scope)
	if err != nil {
		return SessionDelegationSnapshot{}, err
	}
	snapshot, err := sessionDelegationSnapshotTx(ctx, tx, record)
	if err != nil {
		return snapshot, err
	}
	return snapshot, tx.Commit()
}

func (s *Store) ApplySessionDelegationCommand(ctx context.Context, owner, frameID string, command SessionDelegationCommand) (SessionDelegationMutation, error) {
	result := SessionDelegationMutation{Schema: SessionDelegationMutationSchema}
	if s == nil || s.db == nil || ctx == nil {
		return result, ErrSessionDelegationUnavailable
	}
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(frameID) == "" || validateSessionDelegationCommand(command) != nil {
		return result, ErrSessionDelegationInvalid
	}
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		scope, project, err := sessionDelegationScopeTx(ctx, tx, owner, frameID)
		if err != nil {
			return err
		}
		if scope != command.Scope {
			return ErrSessionDelegationScopeStale
		}
		record, previous, err := sessionDelegationPolicyRecordTx(ctx, tx, owner, scope)
		if err != nil {
			return err
		}
		result.Snapshot, err = sessionDelegationSnapshotTx(ctx, tx, record)
		if err != nil {
			return err
		}
		receipt, replayed, err := sessionDelegationReceiptTx(ctx, tx, owner, command)
		if err != nil {
			return err
		}
		if replayed {
			// Return the original applied receipt and the separately observed current
			// policy. In particular, an old deny retry cannot undo a later allow.
			result.Receipt, result.Replayed = receipt, true
			return nil
		}
		if command.ExpectedRevision != record.Revision {
			return &SessionDelegationRevisionError{Snapshot: result.Snapshot}
		}
		now := s.now().UTC()
		record.Policy, record.Revision = command.DesiredPolicy, record.Revision+1
		if err := writeDelegatedRecord(ctx, tx, scope.RootFrameID, sessionDelegationPolicyID(owner, scope), sessionDelegationPolicyEventType, previous, record, now); err != nil {
			return err
		}
		result.Receipt = SessionDelegationReceipt{Schema: SessionDelegationReceiptSchema, Command: command,
			AppliedRevision: record.Revision, AppliedPolicy: record.Policy, AppliedAt: now}
		if err := writeDelegatedRecord(ctx, tx, scope.RootFrameID, sessionDelegationCommandID(owner, command), sessionDelegationCommandEventType, "",
			sessionDelegationCommandRecord{Version: 1, OwnerID: owner, Receipt: result.Receipt}, now); err != nil {
			return err
		}
		result.Snapshot.Policy, result.Snapshot.Revision = record.Policy, record.Revision
		_, err = s.enqueueRealtimeOutboxTransaction(ctx, tx, RealtimeEventInput{
			ID:     delegatedRecordID("session-policy-notification", owner, scope.RootIncarnationID, command.CommandID),
			UserID: owner, ProjectID: project, RootFrameID: scope.RootFrameID, FrameID: scope.RootFrameID,
			Type: "conversation.listChanged", Payload: map[string]any{
				"conversation_id": scope.RootFrameID, "action": "updated", "source": "xscience",
				"delegation_policy_changed": true, "root_frame_id": scope.RootFrameID,
				"root_incarnation_id": scope.RootIncarnationID, "delegation_policy_revision": record.Revision,
			},
		}, "", "")
		return err
	})
	if err != nil {
		return SessionDelegationMutation{}, err
	}
	return result, nil
}

// Called only after accepted-admission replay, inside the child-creation write
// transaction. A denial controls first insertion, never existing child lifetime.
func assertNewSessionDelegationAllowedTx(ctx context.Context, tx workspaceTransaction, owner, frameID string) error {
	scope, _, err := sessionDelegationScopeTx(ctx, tx, owner, frameID)
	if err != nil {
		return err
	}
	record, _, err := sessionDelegationPolicyRecordTx(ctx, tx, owner, scope)
	if err != nil {
		return err
	}
	if record.Policy == "deny" {
		return ErrSessionDelegationDisabled
	}
	return nil
}
