package workspace

import (
	"context"
	"time"
)

// Task provenance is written from trusted execution scope, never bibliography.
type LiteratureTaskOrigin struct {
	Kind                   string `json:"kind"`
	FrameID                string `json:"frame_id"`
	FrameIncarnationID     string `json:"frame_incarnation_id"`
	RootFrameID            string `json:"root_frame_id"`
	RootFrameIncarnationID string `json:"root_frame_incarnation_id"`
	ProjectID              string `json:"project_id,omitempty"`
	ProjectAvailable       *bool  `json:"project_available,omitempty"`
	RecordedAt             string `json:"recorded_at"`
}

func readLiteratureTaskOrigins(ctx context.Context, q literatureItemQuery, owner, candidate string) ([]LiteratureTaskOrigin, error) {
	rows, err := q.QueryContext(ctx, `SELECT origin.frame_id,origin.frame_incarnation_id,origin.root_frame_id,
 origin.root_frame_incarnation_id,origin.project_id,origin.recorded_at,
 EXISTS(SELECT 1 FROM projects p WHERE p.id=origin.project_id AND p.user_id=origin.owner_user_id)
 FROM literature_candidate_task_origins origin JOIN literature_candidates candidate ON candidate.id=origin.candidate_id AND candidate.owner_user_id=origin.owner_user_id
 WHERE origin.owner_user_id=? AND origin.candidate_id=? ORDER BY origin.recorded_at,origin.frame_incarnation_id`, owner, candidate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []LiteratureTaskOrigin
	for rows.Next() {
		origin := LiteratureTaskOrigin{Kind: "agent"}
		var available bool
		if err := rows.Scan(&origin.FrameID, &origin.FrameIncarnationID, &origin.RootFrameID, &origin.RootFrameIncarnationID, &origin.ProjectID, &origin.RecordedAt, &available); err != nil {
			return nil, err
		}
		if origin.ProjectID != "" {
			origin.ProjectAvailable = &available
		}
		result = append(result, origin)
	}
	return result, rows.Err()
}

func (s *Store) recordLiteratureTaskOriginTx(ctx context.Context, tx workspaceTransaction, scope LiteratureTaskScope, candidate LiteratureCandidate) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO literature_candidate_task_origins
 (owner_user_id,candidate_id,frame_id,frame_incarnation_id,root_frame_id,root_frame_incarnation_id,project_id,recorded_at)
 VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(owner_user_id,candidate_id,frame_incarnation_id) DO NOTHING`,
		scope.OwnerID, candidate.ID, scope.FrameID, scope.FrameIncarnationID, scope.RootFrameID, scope.RootFrameIncarnationID,
		scope.ProjectID, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	var recorded LiteratureTaskScope
	err = tx.QueryRowContext(ctx, `SELECT frame_id,root_frame_id,root_frame_incarnation_id,project_id
 FROM literature_candidate_task_origins WHERE owner_user_id=? AND candidate_id=? AND frame_incarnation_id=?`, scope.OwnerID, candidate.ID, scope.FrameIncarnationID).
		Scan(&recorded.FrameID, &recorded.RootFrameID, &recorded.RootFrameIncarnationID, &recorded.ProjectID)
	if err != nil {
		return err
	}
	if recorded.FrameID != scope.FrameID || recorded.RootFrameID != scope.RootFrameID || recorded.RootFrameIncarnationID != scope.RootFrameIncarnationID || recorded.ProjectID != scope.ProjectID {
		return ErrLiteratureConflict
	}
	// Reusing an active item links only this discovery's current project. It
	// never reapplies earlier projects which the user may have removed later.
	if candidate.State == "accepted" && scope.ProjectID != "" {
		return s.setLiteratureProjectTx(ctx, tx, scope.OwnerID, candidate.ItemID, scope.ProjectID, true)
	}
	return nil
}

func (s *Store) linkLiteratureTaskDestinationsTx(ctx context.Context, tx workspaceTransaction, owner, candidate, item string) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT origin.project_id FROM literature_candidate_task_origins origin
 JOIN projects p ON p.id=origin.project_id AND p.user_id=origin.owner_user_id
 WHERE origin.owner_user_id=? AND origin.candidate_id=? ORDER BY origin.project_id`, owner, candidate)
	if err != nil {
		return err
	}
	var projects []string
	for rows.Next() {
		var project string
		if err = rows.Scan(&project); err != nil {
			break
		}
		projects = append(projects, project)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, project := range projects {
		if err := s.setLiteratureProjectTx(ctx, tx, owner, item, project, true); err != nil {
			return err
		}
	}
	return nil
}
