package workspace

import (
	"context"
	"database/sql"
	"errors"
)

// ManualKernelExecutionScopeRetired proves that the original frame identity no
// longer exists. It is only a cleanup proof for an already terminal handle;
// owner mismatch, missing lifecycle records, and a new boot are not proof that
// physical execution has stopped.
func (s *Store) ManualKernelExecutionScopeRetired(ctx context.Context, record ManualKernelExecution) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrWorkspaceStoreClosed
	}
	if err := validateManualKernelExecution(record); err != nil {
		return false, err
	}
	var frameIncarnation, rootID string
	var rootIncarnation sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT frame.incarnation_id,frame.root_frame_id,root.incarnation_id
		FROM frames frame LEFT JOIN frames root ON root.id=frame.root_frame_id
		WHERE frame.id=?`, record.FrameID).Scan(&frameIncarnation, &rootID, &rootIncarnation)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return frameIncarnation != record.FrameIncarnationID || rootID != record.RootFrameID ||
		!rootIncarnation.Valid || rootIncarnation.String != record.RootIncarnationID, nil
}
