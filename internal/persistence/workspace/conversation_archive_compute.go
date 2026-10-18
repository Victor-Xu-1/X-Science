package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Project-only jobs have no invented conversation owner. For bound jobs, use
// the real Frame/root rather than trusting a caller's supplied root label.
func checkComputeConversationAvailableTx(ctx context.Context, tx workspaceTransaction, ownerID string, job ComputeJob) error {
	frameID := ""
	if job.FrameID != nil {
		frameID = strings.TrimSpace(*job.FrameID)
	}
	if frameID == "" && job.RootFrameID != nil {
		frameID = strings.TrimSpace(*job.RootFrameID)
	}
	if frameID == "" {
		return nil
	}
	var rootID string
	err := tx.QueryRowContext(ctx, `SELECT f.root_frame_id FROM frames f JOIN projects p ON p.id=f.project_id
		WHERE f.id=? AND p.id=? AND p.user_id=?`, frameID, job.ProjectID, ownerID).Scan(&rootID)
	if errors.Is(err, sql.ErrNoRows) {
		return transcriptstore.ErrOwnerMismatch
	}
	if err != nil {
		return err
	}
	if job.RootFrameID != nil && strings.TrimSpace(*job.RootFrameID) != "" && strings.TrimSpace(*job.RootFrameID) != rootID {
		return transcriptstore.ErrEventConflict
	}
	return transcriptstore.CheckFrameConversationAvailable(ctx, tx, frameID)
}
