package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const SessionPermissionGrantLimit = 1000

type SessionPermissionGrant struct {
	GrantID     string `json:"grant_id"`
	Kind        string `json:"kind"`
	Key         string `json:"key"`
	CategoryKey string `json:"category_key"`
	Label       string `json:"label"`
	Scope       string `json:"scope"`
	Tier        string `json:"tier"`
}

type SessionPermissionGrantSnapshot struct {
	Schema             string                   `json:"schema"`
	Scope              SessionPermissionScope   `json:"scope"`
	Grants             []SessionPermissionGrant `json:"grants"`
	LegacyUnboundCount int                      `json:"legacy_unbound_count"`
	StaleGrantCount    int                      `json:"stale_grant_count"`
	Incomplete         bool                     `json:"incomplete"`
	IncompleteReason   string                   `json:"incomplete_reason,omitempty"`
}

func (s *Store) SessionPermissionGrants(ctx context.Context, owner, frameID string, expected *SessionPermissionExpectedScope) (SessionPermissionGrantSnapshot, error) {
	if s == nil || s.readDatabase() == nil || ctx == nil {
		return SessionPermissionGrantSnapshot{}, errors.New("session grant storage/context unavailable")
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SessionPermissionGrantSnapshot{}, err
	}
	defer tx.Rollback()
	scope, err := sessionPermissionScopeTx(ctx, tx, owner, frameID, expected)
	if err != nil {
		return SessionPermissionGrantSnapshot{}, err
	}
	result, err := sessionPermissionGrantsTx(ctx, tx, owner, scope)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func sessionPermissionGrantsTx(ctx context.Context, tx workspaceTransaction, owner string, scope SessionPermissionScope) (SessionPermissionGrantSnapshot, error) {
	result := SessionPermissionGrantSnapshot{Schema: "x-science.session-permission-grants.v1", Scope: scope, Grants: []SessionPermissionGrant{}}
	rows, err := tx.QueryContext(ctx, `SELECT id,grant_key,tier FROM approval_policy_grants
		WHERE owner_user_id=? AND kind='local_exec' AND scope='conversation' AND scope_target_id=?
		ORDER BY grant_key,id LIMIT ?`, owner, scope.RootFrameID, SessionPermissionGrantLimit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	scanned := 0
	for rows.Next() {
		var id, key, tier string
		if err := rows.Scan(&id, &key, &tier); err != nil {
			return result, err
		}
		scanned++
		if scanned > SessionPermissionGrantLimit {
			result.Incomplete, result.IncompleteReason = true, "limit"
			break
		}
		if !strings.HasPrefix(id, "apg1.") {
			result.LegacyUnboundCount++
			continue
		}
		if !sessionGrantMatchesBinding(id, owner, scope.ProjectID, scope.RootFrameID, scope.RootFrameIncarnationID, key) {
			result.StaleGrantCount++
			continue
		}
		if tier != "allow" {
			continue
		}
		result.Grants = append(result.Grants, SessionPermissionGrant{GrantID: id, Kind: "local_exec", Key: key,
			CategoryKey: "local_exec:" + key, Label: key, Scope: "conversation", Tier: "allow"})
	}
	return result, rows.Err()
}
