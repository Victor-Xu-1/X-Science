package workspace

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	transcriptstore "x-science/internal/persistence/transcript"
)

type SessionPermissionGrantSelection struct {
	GrantID string `json:"grant_id"`
	Kind    string `json:"kind"`
	Key     string `json:"key"`
}

type SessionPermissionGrantRevokeInput struct {
	OwnerID, FrameID string
	Expected         SessionPermissionExpectedScope
	Grants           []SessionPermissionGrantSelection
}

type SessionPermissionGrantResult struct {
	GrantID string `json:"grant_id"`
	Status  string `json:"status"`
}

type SessionPermissionGrantMutation struct {
	Schema                   string                         `json:"schema"`
	Scope                    SessionPermissionScope         `json:"scope"`
	Results                  []SessionPermissionGrantResult `json:"results"`
	Snapshot                 SessionPermissionGrantSnapshot `json:"snapshot"`
	BroaderPoliciesUnchanged bool                           `json:"broader_policies_unchanged"`
}

func (s *Store) RevokeSessionPermissionGrants(ctx context.Context, input SessionPermissionGrantRevokeInput) (SessionPermissionGrantMutation, error) {
	result := SessionPermissionGrantMutation{Schema: "x-science.session-permission-revoke.v1", Results: []SessionPermissionGrantResult{}, BroaderPoliciesUnchanged: true}
	if s == nil || s.db == nil {
		return result, errors.New("session grant storage unavailable")
	}
	if ctx == nil || len(input.Grants) == 0 || len(input.Grants) > SessionPermissionGrantLimit {
		return result, ErrSessionGrantRequestInvalid
	}
	for _, value := range []string{input.Expected.FrameIncarnationID, input.Expected.RootFrameID, input.Expected.RootFrameIncarnationID} {
		if value == "" || len(value) > 512 {
			return result, ErrSessionGrantRequestInvalid
		}
	}
	seen := map[string]bool{}
	for _, grant := range input.Grants {
		if grant.Kind != "local_exec" || grant.Key == "" || len(grant.Key) > 512 || grant.Key != kernelLocalExecGrantKey(grant.Key, "") ||
			grant.GrantID == "" || len(grant.GrantID) > 4096 || seen[grant.Key] {
			return result, ErrSessionGrantRequestInvalid
		}
		seen[grant.Key] = true
	}
	repo, err := s.TranscriptRepository(ctx)
	if err != nil {
		return result, err
	}
	changed := false
	err = repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		scope, err := sessionPermissionScopeTx(ctx, tx, input.OwnerID, input.FrameID, &input.Expected)
		if err != nil {
			return err
		}
		// Validate every selected binding before deleting any row. Scope comes
		// from the same transaction, never from an old HTTP access precheck.
		for _, grant := range input.Grants {
			if !sessionGrantMatchesBinding(grant.GrantID, input.OwnerID, scope.ProjectID, scope.RootFrameID, scope.RootFrameIncarnationID, grant.Key) {
				return ErrSessionGrantRequestInvalid
			}
		}
		result.Scope = scope
		for _, grant := range input.Grants {
			status, err := revokeSessionPermissionGrantTx(ctx, tx, input.OwnerID, scope, grant)
			if err != nil {
				return err
			}
			if status == "revoked" {
				changed = true
			}
			result.Results = append(result.Results, SessionPermissionGrantResult{GrantID: grant.GrantID, Status: status})
		}
		result.Snapshot, err = sessionPermissionGrantsTx(ctx, tx, input.OwnerID, scope)
		if err != nil || !changed {
			return err
		}
		return s.enqueueSessionPermissionGrantChangedTx(ctx, tx, input.OwnerID, scope, "session-permission-revoked:"+uuid.NewString())
	})
	if err != nil {
		return SessionPermissionGrantMutation{}, err
	}
	if changed {
		s.signalKernelRetentionWake()
	}
	return result, nil
}

func revokeSessionPermissionGrantTx(ctx context.Context, tx workspaceTransaction, owner string, scope SessionPermissionScope, grant SessionPermissionGrantSelection) (string, error) {
	var id, tier string
	err := tx.QueryRowContext(ctx, `SELECT id,tier FROM approval_policy_grants
		WHERE owner_user_id=? AND kind='local_exec' AND grant_key=? AND scope='conversation' AND scope_target_id=?`, owner, grant.Key, scope.RootFrameID).Scan(&id, &tier)
	if errors.Is(err, sql.ErrNoRows) {
		return "already_absent", nil
	}
	if err != nil {
		return "", err
	}
	if id != grant.GrantID || tier != "allow" {
		return "changed", nil
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM approval_policy_grants
		WHERE id=? AND owner_user_id=? AND kind='local_exec' AND grant_key=? AND scope='conversation' AND scope_target_id=? AND tier='allow'`,
		grant.GrantID, owner, grant.Key, scope.RootFrameID)
	if err != nil {
		return "", err
	}
	count, err := deleted.RowsAffected()
	if err != nil {
		return "", err
	}
	if count != 1 {
		return "", ErrApprovalPolicyGrantStale
	}
	return "revoked", nil
}
