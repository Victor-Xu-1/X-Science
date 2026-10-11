package delegatefixture

import (
	"context"
	"fmt"

	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

// Clear removes the owned delegation tree while retaining its project entity.
// Project IDs are durable identities, not reusable test slots. Both browser
// cleanup and replacement use this lifecycle; the exported IDs stay stable.
func Clear(store *workspace.Store) (bool, error) {
	owner, found, err := store.ProjectOwnerID(ProjectID)
	if err != nil {
		return false, fmt.Errorf("inspect existing fixture project: %w", err)
	}
	if !found {
		return false, nil
	}
	if owner != UserID {
		return false, fmt.Errorf("fixture project %q belongs to another owner", ProjectID)
	}
	frame, frameFound, err := store.GetCompatibilityFrame(ParentFrameID)
	if err != nil {
		return false, fmt.Errorf("inspect existing fixture frame: %w", err)
	}
	if frameFound {
		if frame.ProjectID != ProjectID || frame.RootFrameID != ParentFrameID || frame.ParentFrameID != "" {
			return false, fmt.Errorf("fixture frame %q is not the owned fixture root", ParentFrameID)
		}
		// Keep the original fixture reset's non-realtime semantics: a realtime
		// retirement outbox would block the immediate bootstrap of the same
		// public frame IDs. This is test support, never production deletion.
		// One existing immediate transaction binds streams and frame cleanup to
		// the exact owned incarnation, retaining unrelated roots and the project.
		ctx := context.Background()
		repository, err := store.TranscriptRepository(ctx)
		if err != nil {
			return false, fmt.Errorf("open fixture transcript repository: %w", err)
		}
		if err := repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
			return clearFixtureRootTx(ctx, tx, frame.IncarnationID)
		}); err != nil {
			return false, fmt.Errorf("clear owned fixture root transcript: %w", err)
		}
	}
	return frameFound, nil
}

func clearFixtureRootTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, incarnationID string) error {
	var matching int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM frames AS frame
		JOIN projects AS project ON project.id=frame.project_id
		WHERE frame.id=? AND frame.project_id=? AND frame.root_frame_id=?
		AND COALESCE(frame.parent_frame_id,'')='' AND frame.incarnation_id=? AND project.user_id=?`,
		ParentFrameID, ProjectID, ParentFrameID, incarnationID, UserID).Scan(&matching); err != nil {
		return err
	}
	if matching != 1 {
		return fmt.Errorf("fixture root changed before transcript cleanup")
	}
	if _, err := transcriptstore.DeleteRootStreamsTx(ctx, tx, UserID, ProjectID, ParentFrameID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM frames
		WHERE project_id=? AND root_frame_id=? AND EXISTS
		(SELECT 1 FROM projects WHERE projects.id=frames.project_id AND projects.user_id=?)`,
		ProjectID, ParentFrameID, UserID)
	return err
}
