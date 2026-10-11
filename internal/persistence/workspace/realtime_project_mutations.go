package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProjectMutationPreparation runs after the row and outbox validate but before
// their transaction commits. It must not call this Store while the transaction
// is held. Return compensation only for newly owned resources. Compensation is
// invoked after failure only when a fresh transactional read proves that no
// durable project row references the prepared path.
type ProjectMutationPreparation func(Project) (PreparedProjectResource, error)

// Release closes observation handles after the commit/compensation decision. It
// never changes durable filesystem state or changes a committed mutation result.
type PreparedProjectResource struct {
	Rollback func() error
	Release  func()
}

func (s *Store) CreateProjectRealtime(ctx context.Context, input CreateProjectInput, eventID string) (Project, error) {
	return s.CreateProjectRealtimeWithPreparation(ctx, input, eventID, nil)
}

func (s *Store) CreateProjectRealtimeWithPreparation(ctx context.Context, input CreateProjectInput, eventID string, prepare ProjectMutationPreparation) (Project, error) {
	eventID = mutationRealtimeID(eventID)
	return s.withPreparedProjectMutation(ctx, func(tx *sql.Tx) (Project, error) {
		project, err := s.CreateProjectOutboxTx(ctx, tx, input)
		if err != nil {
			return project, err
		}
		_, err = s.EnqueueRealtimeOutboxTx(ctx, tx, RealtimeEventInput{
			ID: eventID, UserID: normalizedProjectUserID(input.UserID), ProjectID: project.ID,
			Type: "frame_update", Payload: map[string]any{"project_id": project.ID, "action": "project_created"},
		}, "")
		return project, err
	}, prepare)
}

func (s *Store) UpdateProjectRealtime(ctx context.Context, id, ownerUserID string, input UpdateProjectInput, eventID string) (Project, error) {
	return s.UpdateProjectRealtimeWithPreparation(ctx, id, ownerUserID, input, eventID, nil)
}

func (s *Store) UpdateProjectRealtimeWithPreparation(ctx context.Context, id, ownerUserID string, input UpdateProjectInput, eventID string, prepare ProjectMutationPreparation) (Project, error) {
	eventID = mutationRealtimeID(eventID)
	return s.withPreparedProjectMutation(ctx, func(tx *sql.Tx) (Project, error) {
		project, err := s.UpdateProjectOutboxTx(ctx, tx, id, ownerUserID, input)
		if err != nil {
			return project, err
		}
		_, err = s.EnqueueRealtimeOutboxTx(ctx, tx, RealtimeEventInput{
			ID: eventID, UserID: ownerUserID, ProjectID: project.ID,
			Type: "frame_update", Payload: map[string]any{"project_id": project.ID, "action": "project_updated"},
		}, "")
		return project, err
	}, prepare)
}

func (s *Store) withPreparedProjectMutation(ctx context.Context, mutate func(*sql.Tx) (Project, error), prepare ProjectMutationPreparation) (Project, error) {
	var project Project
	var resource PreparedProjectResource
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		project, err = mutate(tx)
		if err == nil && prepare != nil {
			resource, err = prepare(project)
		}
		return err
	})
	if resource.Release != nil {
		defer resource.Release()
	}
	if err != nil && resource.Rollback != nil {
		err = errors.Join(err, s.compensateProjectPreparation(project, resource.Rollback))
	}
	return project, err
}

func (s *Store) compensateProjectPreparation(project Project, rollback func() error) error {
	// Cancellation must not skip reconciliation, but cleanup is not a long task.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		// BeginTx is deferred in SQLite/WAL. Acquire write intent before the
		// lookup so another process cannot commit a new reference during cleanup.
		// This statement changes no row, timestamp, schema or outbox event.
		if _, err := tx.ExecContext(ctx, "UPDATE projects SET path = path WHERE 0"); err != nil {
			return fmt.Errorf("prepared project workspace retained; write ownership could not be verified: %w", err)
		}
		var references int
		err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE path = ?", project.Path).Scan(&references)
		if err != nil {
			return fmt.Errorf("prepared project workspace retained; persistence outcome could not be verified: %w", err)
		}
		if references > 0 {
			return errors.New("prepared project workspace retained; a durable project still references it")
		}
		if err := rollback(); err != nil {
			return fmt.Errorf("prepared project workspace retained; compensation could not complete: %w", err)
		}
		return nil
	})
}
