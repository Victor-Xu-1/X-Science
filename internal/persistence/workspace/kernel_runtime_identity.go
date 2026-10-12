package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// KernelRuntimeIdentityReceipt records observed identity, not a guessed process
// or container name. It remains attached to the canonical admitted operation.
type KernelRuntimeIdentityReceipt struct {
	OperationID           string
	ExecutionID           string
	InputSHA256           string
	RuntimeKind           string
	RuntimeID             string
	Environment           string
	EnvironmentGeneration string
	ImageID               string
	NamespaceOrigin       string
	CreatedAtMS           int64
}

const kernelRuntimeIdentitySelect = `SELECT identity.operation_id,identity.execution_id,identity.input_sha256,
	identity.runtime_kind,identity.runtime_id,identity.environment,identity.environment_generation,identity.image_id,identity.namespace_origin,identity.created_at_ms
	FROM kernel_runtime_identity_receipts identity JOIN kernel_local_operations operation ON operation.operation_id=identity.operation_id`

func (s *Store) RecordKernelRuntimeIdentity(ctx context.Context, ownerUserID string, receipt KernelRuntimeIdentityReceipt) (KernelRuntimeIdentityReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if receipt.NamespaceOrigin == "" {
		receipt.NamespaceOrigin = "x-science"
	}
	if s == nil || s.db == nil {
		return KernelRuntimeIdentityReceipt{}, errors.New("workspace store is closed")
	}
	if err := validateKernelRuntimeIdentity(ownerUserID, receipt); err != nil {
		return KernelRuntimeIdentityReceipt{}, err
	}
	var recorded KernelRuntimeIdentityReceipt
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		var environment, inputSHA256, executionID, state string
		if err := tx.QueryRowContext(ctx, `SELECT environment,input_sha256,execution_id,state FROM kernel_local_operations
			WHERE operation_id=? AND owner_user_id=?`, receipt.OperationID, ownerUserID).Scan(&environment, &inputSHA256, &executionID, &state); err != nil {
			return err
		}
		if state != KernelLocalOperationStateStarted || executionID != receipt.ExecutionID || inputSHA256 != receipt.InputSHA256 || environment != receipt.Environment {
			return errors.New("kernel runtime identity conflicts with the started operation authority")
		}
		if receipt.NamespaceOrigin == "pre-cutover" {
			var frozen bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM namespace_kernel_operation_receipts
				WHERE operation_id=? AND original_state='started' AND execution_id=? AND input_sha256=? AND environment=?)`, receipt.OperationID, receipt.ExecutionID, receipt.InputSHA256, receipt.Environment).Scan(&frozen); err != nil {
				return err
			}
			if !frozen {
				return errors.New("historical runtime identity is not bound to a started cutover execution")
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO kernel_runtime_identity_receipts
			(operation_id,execution_id,input_sha256,runtime_kind,runtime_id,environment,environment_generation,image_id,namespace_origin,created_at_ms)
			VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(operation_id) DO NOTHING`, receipt.OperationID, receipt.ExecutionID, receipt.InputSHA256,
			receipt.RuntimeKind, receipt.RuntimeID, receipt.Environment, receipt.EnvironmentGeneration, receipt.ImageID, receipt.NamespaceOrigin, s.now().UTC().UnixMilli()); err != nil {
			return err
		}
		var err error
		recorded, err = scanKernelRuntimeIdentity(tx.QueryRowContext(ctx, kernelRuntimeIdentitySelect+` WHERE identity.operation_id=? AND operation.owner_user_id=?`, receipt.OperationID, ownerUserID))
		if err != nil {
			return err
		}
		if !kernelRuntimeIdentityMatches(recorded, receipt) {
			return errors.New("kernel runtime identity receipt conflicts with immutable recorded identity")
		}
		return nil
	})
	return recorded, err
}

func (s *Store) GetKernelRuntimeIdentity(ctx context.Context, ownerUserID, operationID string) (KernelRuntimeIdentityReceipt, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return KernelRuntimeIdentityReceipt{}, false, errors.New("workspace store is closed")
	}
	if !namespaceIdentityValid(ownerUserID) || !namespaceIdentityValid(operationID) {
		return KernelRuntimeIdentityReceipt{}, false, errors.New("owner and operation identity are required")
	}
	identity, err := scanKernelRuntimeIdentity(s.db.QueryRowContext(ctx, kernelRuntimeIdentitySelect+` WHERE identity.operation_id=? AND operation.owner_user_id=?`, operationID, ownerUserID))
	if errors.Is(err, sql.ErrNoRows) {
		return KernelRuntimeIdentityReceipt{}, false, nil
	}
	return identity, err == nil, err
}

func validateKernelRuntimeIdentity(owner string, receipt KernelRuntimeIdentityReceipt) error {
	for name, value := range map[string]string{"owner": owner, "operation": receipt.OperationID, "execution": receipt.ExecutionID,
		"runtime kind": receipt.RuntimeKind, "runtime id": receipt.RuntimeID, "environment": receipt.Environment, "generation": receipt.EnvironmentGeneration} {
		if value == "" || len(value) > 512 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("kernel runtime identity %s is invalid", name)
		}
	}
	if !validLowerHexSHA256(receipt.InputSHA256) || len(receipt.RuntimeKind) > 64 || len(receipt.ImageID) > 512 || strings.ContainsAny(receipt.ImageID, "\x00\r\n") {
		return errors.New("kernel runtime identity input/image binding is invalid")
	}
	if receipt.NamespaceOrigin != "x-science" && receipt.NamespaceOrigin != "pre-cutover" {
		return errors.New("kernel runtime namespace origin is invalid")
	}
	return nil
}

func scanKernelRuntimeIdentity(row interface{ Scan(...any) error }) (KernelRuntimeIdentityReceipt, error) {
	var receipt KernelRuntimeIdentityReceipt
	err := row.Scan(&receipt.OperationID, &receipt.ExecutionID, &receipt.InputSHA256, &receipt.RuntimeKind, &receipt.RuntimeID,
		&receipt.Environment, &receipt.EnvironmentGeneration, &receipt.ImageID, &receipt.NamespaceOrigin, &receipt.CreatedAtMS)
	return receipt, err
}

func kernelRuntimeIdentityMatches(left, right KernelRuntimeIdentityReceipt) bool {
	left.CreatedAtMS, right.CreatedAtMS = 0, 0
	return left == right
}
