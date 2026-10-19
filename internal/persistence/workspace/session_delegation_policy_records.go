package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const maxSessionDelegationRevision int64 = 1<<53 - 1

type sessionDelegationPolicyRecord struct {
	Version  int                    `json:"version"`
	OwnerID  string                 `json:"owner_id"`
	Scope    SessionDelegationScope `json:"scope"`
	Policy   string                 `json:"policy"`
	Revision int64                  `json:"revision"`
}

type sessionDelegationCommandRecord struct {
	Version int                      `json:"version"`
	OwnerID string                   `json:"owner_id"`
	Receipt SessionDelegationReceipt `json:"receipt"`
}

func validSessionDelegationPolicy(policy string) bool { return policy == "allow" || policy == "deny" }

func validateSessionDelegationCommand(command SessionDelegationCommand) error {
	if command.Schema != SessionDelegationCommandSchema || !validSessionDelegationPolicy(command.DesiredPolicy) ||
		command.CommandID == "" || len(command.CommandID) > 256 || strings.TrimSpace(command.CommandID) != command.CommandID ||
		strings.IndexFunc(command.CommandID, unicode.IsControl) >= 0 || command.ExpectedRevision < 0 || command.ExpectedRevision >= maxSessionDelegationRevision {
		return ErrSessionDelegationInvalid
	}
	for _, value := range []string{command.Scope.RootFrameID, command.Scope.RootIncarnationID} {
		if value == "" || len(value) > 512 || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return ErrSessionDelegationInvalid
		}
	}
	return nil
}

func sessionDelegationScopeTx(ctx context.Context, tx workspaceTransaction, owner, frameID string) (SessionDelegationScope, string, error) {
	scope, project, err := delegatedScopeTx(ctx, tx, owner, frameID)
	if errors.Is(err, ErrDelegatedStopUnavailable) {
		return SessionDelegationScope{}, "", ErrSessionDelegationTarget
	}
	if errors.Is(err, ErrDelegatedStopScopeStale) {
		return SessionDelegationScope{}, "", ErrSessionDelegationScopeStale
	}
	if err != nil {
		return SessionDelegationScope{}, "", fmt.Errorf("%w: resolve root lifetime: %w", ErrSessionDelegationUnavailable, err)
	}
	return SessionDelegationScope{RootFrameID: scope.RootFrameID, RootIncarnationID: scope.RootFrameIncarnationID}, project, nil
}

func sessionDelegationPolicyID(owner string, scope SessionDelegationScope) string {
	return delegatedRecordID("session-policy", owner, scope.RootFrameID, scope.RootIncarnationID)
}

func sessionDelegationCommandID(owner string, command SessionDelegationCommand) string {
	return delegatedRecordID("session-policy-command", owner, command.Scope.RootFrameID, command.Scope.RootIncarnationID, command.CommandID)
}

func sessionDelegationPolicyRecordTx(ctx context.Context, tx workspaceTransaction, owner string, scope SessionDelegationScope) (sessionDelegationPolicyRecord, string, error) {
	var record sessionDelegationPolicyRecord
	raw, found, err := readDelegatedRecord(ctx, tx, scope.RootFrameID, sessionDelegationPolicyID(owner, scope), sessionDelegationPolicyEventType, &record)
	if err != nil {
		return record, "", fmt.Errorf("%w: read policy: %w", ErrSessionDelegationUnavailable, err)
	}
	if !found {
		// Only a never-established policy has the legacy allow default. A retained
		// command without its head, or a mismatched lifetime record, is corruption.
		var established bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM frame_events WHERE frame_id=? AND event_type IN (?,?))`,
			scope.RootFrameID, sessionDelegationPolicyEventType, sessionDelegationCommandEventType).Scan(&established); err != nil {
			return record, "", fmt.Errorf("%w: inspect retained policy records: %w", ErrSessionDelegationUnavailable, err)
		}
		if established {
			return record, "", ErrSessionDelegationUnavailable
		}
		return sessionDelegationPolicyRecord{Version: 1, OwnerID: owner, Scope: scope, Policy: "allow"}, "", nil
	}
	if record.Version != 1 || record.OwnerID != owner || record.Scope != scope || !validSessionDelegationPolicy(record.Policy) || record.Revision < 1 || record.Revision > maxSessionDelegationRevision {
		return record, "", ErrSessionDelegationUnavailable
	}
	return record, raw, nil
}

func sessionDelegationReceiptTx(ctx context.Context, tx workspaceTransaction, owner string, command SessionDelegationCommand) (SessionDelegationReceipt, bool, error) {
	var record sessionDelegationCommandRecord
	_, found, err := readDelegatedRecord(ctx, tx, command.Scope.RootFrameID, sessionDelegationCommandID(owner, command), sessionDelegationCommandEventType, &record)
	if err != nil {
		return SessionDelegationReceipt{}, false, fmt.Errorf("%w: read command: %w", ErrSessionDelegationUnavailable, err)
	}
	if !found {
		return SessionDelegationReceipt{}, false, nil
	}
	receipt := record.Receipt
	if record.Version != 1 || record.OwnerID != owner || receipt.Schema != SessionDelegationReceiptSchema ||
		validateSessionDelegationCommand(receipt.Command) != nil || receipt.Command.Scope != command.Scope || receipt.Command.CommandID != command.CommandID ||
		receipt.AppliedRevision != receipt.Command.ExpectedRevision+1 || receipt.AppliedPolicy != receipt.Command.DesiredPolicy || receipt.AppliedAt.IsZero() {
		return receipt, false, ErrSessionDelegationUnavailable
	}
	if receipt.Command != command {
		return receipt, false, ErrSessionDelegationConflict
	}
	return receipt, true, nil
}

func sessionDelegationLiveAttemptsTx(ctx context.Context, tx workspaceTransaction, owner string, scope SessionDelegationScope) (bool, error) {
	var available bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='kernel_child_supervision')`).Scan(&available); err != nil {
		return false, err
	}
	if !available {
		return false, nil
	}
	var live bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM kernel_child_supervision
		WHERE root_frame_id=? AND owner_user_id=? AND status IN ('processing','awaiting_user_response'))`, scope.RootFrameID, owner).Scan(&live)
	return live, err
}

func sessionDelegationSnapshotTx(ctx context.Context, tx workspaceTransaction, record sessionDelegationPolicyRecord) (SessionDelegationSnapshot, error) {
	live, err := sessionDelegationLiveAttemptsTx(ctx, tx, record.OwnerID, record.Scope)
	if err != nil {
		return SessionDelegationSnapshot{}, fmt.Errorf("%w: read live attempts: %w", ErrSessionDelegationUnavailable, err)
	}
	return SessionDelegationSnapshot{Schema: SessionDelegationPolicySchema, Scope: record.Scope,
		Policy: record.Policy, Revision: record.Revision, HasLiveDelegatedAttempts: live}, nil
}
