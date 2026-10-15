package workspace

import (
	"context"
	"fmt"

	transcriptstore "x-science/internal/persistence/transcript"
)

func validateKernelRecoveryAdmissionSource(ctx context.Context, query transcriptstore.InputAdmissionSourceQuery, source transcriptstore.RecoveryAdmissionSource) error {
	var matches bool
	if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM kernel_local_operation_transitions t
		JOIN kernel_local_operations o ON o.operation_id=t.operation_id
		WHERE t.operation_id=? AND t.state_version=? AND t.transition_seq=t.state_version
		AND t.admitted_input_revision=? AND o.stream_uid=? AND o.owner_user_id=? AND o.project_id=? AND o.frame_id=?
		AND o.branch_id=? AND o.branch_generation=?
		AND ((t.from_state='prepared' AND t.to_state='approved') OR (t.from_state='started' AND t.to_state='outcome_unknown'))
		AND t.admitted_input_revision>COALESCE((SELECT p.admitted_input_revision FROM kernel_local_operation_transitions p
			WHERE p.operation_id=t.operation_id AND p.state_version=t.state_version-1),0))`,
		source.OperationID, source.StateVersion, source.InputRevision, source.StreamUID, source.OwnerID, source.ProjectID,
		source.FrameID, source.BranchID, source.BranchGeneration).Scan(&matches); err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("input admission recovery source differs: %w", transcriptstore.ErrEventConflict)
	}
	return nil
}

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
