package workspace

import (
	"context"
	"database/sql"
)

// This read supplies dispatch's frozen input fence, not authority to execute.
// ClaimRunner repeats the input/incarnation check in its final transaction.
func (s *Store) KernelChildDispatchTarget(ctx context.Context, owner, parent, child string) (DelegatedStopTarget, string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DelegatedStopTarget{}, "", err
	}
	defer tx.Rollback()
	scope, _, err := delegatedScopeTx(ctx, tx, owner, parent)
	if err != nil {
		return DelegatedStopTarget{}, "", err
	}
	target, status, err := delegatedStopTargetWithPendingTx(ctx, tx, owner, scope, child, true)
	if err != nil {
		return target, status, err
	}
	return target, status, tx.Commit()
}
