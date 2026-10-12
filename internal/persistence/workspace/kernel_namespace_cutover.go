package workspace

import (
	"context"
	"database/sql"
	"errors"
)

type KernelNamespaceCutoverReceipt struct {
	OperationID      string
	OriginalState    string
	ExecutionID      string
	InputSHA256      string
	Environment      string
	KernelID         string
	KernelGeneration int64
	StartedAt        string
}

// GetKernelNamespaceCutoverReceipt distinguishes a job already started at the
// cutover from an old admitted operation that legitimately starts new work.
func (s *Store) GetKernelNamespaceCutoverReceipt(ctx context.Context, ownerUserID, operationID string) (KernelNamespaceCutoverReceipt, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return KernelNamespaceCutoverReceipt{}, false, errors.New("workspace store is closed")
	}
	if !namespaceIdentityValid(ownerUserID) || !namespaceIdentityValid(operationID) {
		return KernelNamespaceCutoverReceipt{}, false, errors.New("owner and operation identity are required")
	}
	var receipt KernelNamespaceCutoverReceipt
	err := s.db.QueryRowContext(ctx, `SELECT receipt.operation_id,receipt.original_state,receipt.execution_id,receipt.input_sha256,
		receipt.environment,receipt.kernel_id,receipt.kernel_generation,receipt.started_at
		FROM namespace_kernel_operation_receipts receipt JOIN kernel_local_operations operation ON operation.operation_id=receipt.operation_id
		WHERE receipt.operation_id=? AND operation.owner_user_id=?`, operationID, ownerUserID).Scan(&receipt.OperationID,
		&receipt.OriginalState, &receipt.ExecutionID, &receipt.InputSHA256, &receipt.Environment, &receipt.KernelID, &receipt.KernelGeneration, &receipt.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return KernelNamespaceCutoverReceipt{}, false, nil
	}
	return receipt, err == nil, err
}
