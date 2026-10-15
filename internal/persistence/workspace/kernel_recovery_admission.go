package workspace

import (
	"context"

	transcriptstore "x-science/internal/persistence/transcript"
)

func recordKernelRecoveryAdmissionTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, operation KernelLocalOperation) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kernel_local_operation_transitions t
		WHERE t.operation_id=? AND t.state_version=? AND t.transition_seq=t.state_version
		AND t.admitted_input_revision=? AND ((t.from_state='prepared' AND t.to_state='approved')
		OR (t.from_state='started' AND t.to_state='outcome_unknown'))
		AND t.admitted_input_revision>COALESCE((SELECT p.admitted_input_revision FROM kernel_local_operation_transitions p
			WHERE p.operation_id=t.operation_id AND p.state_version=t.state_version-1),0)`,
		operation.OperationID, operation.StateVersion, operation.AdmittedInputRevision).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return ErrKernelLocalOperationConflict
	}
	return tx.RecordKernelRecoveryAdmission(ctx, operation.StreamUID, operation.AdmittedInputRevision,
		operation.OperationID, operation.StateVersion)
}
