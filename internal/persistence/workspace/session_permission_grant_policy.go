package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ApprovalPolicyGrantProof is a selected existing grant, not permission to
// issue a new grant. Consumers must check it in their final admission TX.
type ApprovalPolicyGrantProof struct {
	ID, OwnerID, ProjectID, RootFrameID, RootFrameIncarnationID string
	Kind, Key, Scope, ScopeTargetID, Tier                       string
}

func (s *Store) readKernelLocalExecPolicy(ctx context.Context, owner, project, root, tool, environment string) (ApprovalPolicyDecision, error) {
	if s == nil || s.readDatabase() == nil || ctx == nil {
		return ApprovalPolicyDecision{}, errors.New("approval policy storage/context unavailable")
	}
	owner, project, root = strings.TrimSpace(owner), strings.TrimSpace(project), strings.TrimSpace(root)
	key := kernelLocalExecGrantKey(tool, environment)
	if owner == "" || project == "" || root == "" || key == "" || strings.TrimSpace(environment) == "" {
		return ApprovalPolicyDecision{}, ErrSessionGrantRequestInvalid
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ApprovalPolicyDecision{}, err
	}
	defer tx.Rollback()
	scope, err := sessionPermissionScopeTx(ctx, tx, owner, root, nil)
	if err != nil {
		return ApprovalPolicyDecision{}, err
	}
	if scope.ProjectID != project || scope.RootFrameID != root {
		return ApprovalPolicyDecision{}, ErrSessionGrantScopeStale
	}
	result, err := selectLocalExecPolicyTx(ctx, tx, owner, scope, key)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func selectLocalExecPolicyTx(ctx context.Context, tx workspaceTransaction, owner string, scope SessionPermissionScope, key string) (ApprovalPolicyDecision, error) {
	project, root := scope.ProjectID, scope.RootFrameID
	rows, err := tx.QueryContext(ctx, `SELECT id,tier,scope,scope_target_id FROM approval_policy_grants
		WHERE owner_user_id=? AND kind='local_exec' AND grant_key=? AND (
			(scope='conversation' AND scope_target_id=?) OR (scope='project' AND scope_target_id=?) OR (scope='always' AND scope_target_id=''))
		ORDER BY CASE scope WHEN 'conversation' THEN 0 WHEN 'project' THEN 1 ELSE 2 END LIMIT 3`, owner, key, root, project)
	if err != nil {
		return ApprovalPolicyDecision{}, err
	}
	var result ApprovalPolicyDecision
	for rows.Next() {
		proof := ApprovalPolicyGrantProof{OwnerID: owner, ProjectID: project, RootFrameID: root, RootFrameIncarnationID: scope.RootFrameIncarnationID, Kind: "local_exec", Key: key}
		if err := rows.Scan(&proof.ID, &proof.Tier, &proof.Scope, &proof.ScopeTargetID); err != nil {
			rows.Close()
			return result, err
		}
		if proof.Scope == "conversation" && !sessionGrantMatchesBinding(proof.ID, owner, project, root, scope.RootFrameIncarnationID, key) {
			continue
		}
		if !result.Found {
			result = ApprovalPolicyDecision{Found: true, Tier: proof.Tier, Scope: proof.Scope, ScopeTargetID: proof.ScopeTargetID, GrantProof: &proof}
		}
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func validateApprovalPolicyGrantTx(ctx context.Context, tx workspaceTransaction, proof *ApprovalPolicyGrantProof, owner, project, root, incarnation, tool string, approved bool) error {
	if proof == nil || proof.ID == "" || proof.OwnerID != owner || proof.ProjectID != project || proof.RootFrameID != root ||
		proof.RootFrameIncarnationID != incarnation || proof.Kind != "local_exec" || proof.Key != kernelLocalExecGrantKey(tool, "") ||
		(approved && proof.Tier != "allow") || (!approved && proof.Tier != "deny") {
		return ErrApprovalPolicyGrantStale
	}
	scope, err := sessionPermissionScopeTx(ctx, tx, owner, root, nil)
	if err != nil {
		return err
	}
	if scope.ProjectID != project || scope.RootFrameID != root || scope.RootFrameIncarnationID != incarnation {
		return ErrApprovalPolicyGrantStale
	}
	current, err := selectLocalExecPolicyTx(ctx, tx, owner, scope, proof.Key)
	if err != nil {
		return err
	}
	if !current.Found || current.GrantProof == nil || *current.GrantProof != *proof {
		return ErrApprovalPolicyGrantStale
	}
	return nil
}
