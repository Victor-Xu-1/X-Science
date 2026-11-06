package server

import (
	"context"
	"errors"
	"sync"
	workspace "x-science/internal/persistence/workspace"
)

func studyBatchIssue(err error) string {
	switch {
	case errors.Is(err, workspace.ErrLiteratureStudySourceChanged):
		return "source-changed"
	case errors.Is(err, workspace.ErrLiteratureStudyPublicationConflict):
		return "publication-conflict"
	case errors.Is(err, workspace.ErrLiteratureUnavailable):
		return "item-unavailable"
	case errors.Is(err, errLiteratureStudySourceUnavailable):
		return "source-unavailable"
	case errors.Is(err, errLiteratureStudyInputTooLarge):
		return "input-too-large"
	case errors.Is(err, errLiteratureStudyBusy):
		return "inference-busy"
	case errors.Is(err, ErrRuntimeDraining):
		return "runtime-draining"
	default:
		return "generation-failed"
	}
}

// This joined wave ends with its caller. The checkpoint records progress; only
// the original study service may infer and its Store transaction may publish.
func (s *Server) advanceLiteratureStudyBatch(ctx context.Context, owner string, command workspace.LiteratureStudyBatchCommand,
	evidence func(context.Context, workspace.LiteratureStudySource) (literatureStudyEvidence, error), infer literatureStudyInfer) (*workspace.LiteratureStudyBatch, error) {
	store := s.workspaceStore
	claim, batch, err := store.ClaimLiteratureStudyBatch(ctx, owner, command, literatureStudyDeadline+literatureStudyBatchSettlement)
	if err != nil || claim.Token == "" {
		return batch, err
	}
	var group sync.WaitGroup
	errorsCh := make(chan error, len(claim.Rows))
	for _, index := range claim.Rows {
		row := batch.Rows[index]
		group.Add(1)
		go func() {
			defer group.Done()
			found, err := store.ReconcileLiteratureStudyBatchRow(ctx, owner, claim, index)
			if err == nil && !found {
				_, err = s.generateLiteratureStudyWithPublication(ctx, owner, row.ItemID, row.Request, evidence, infer,
					func(ctx context.Context, snapshot workspace.LiteratureStudySnapshot) (workspace.LiteratureStudyView, error) {
						return store.PublishLiteratureStudyBatchRow(ctx, owner, claim, index, snapshot)
					})
			}
			if err == nil || ctx.Err() != nil {
				return
			}
			if settlement := store.FailLiteratureStudyBatchRow(ctx, owner, claim, index, studyBatchIssue(err), false); settlement != nil {
				errorsCh <- settlement
			}
		}()
	}
	group.Wait()
	close(errorsCh)
	// Cancellation must still relinquish this exact token, with a finite joined
	// write budget. A failure here leaves a bounded lease, never a detached retry.
	settlement, cancel := context.WithTimeout(context.WithoutCancel(ctx), literatureStudyBatchSettlement)
	finishErr := store.FinishLiteratureStudyBatch(settlement, owner, claim, ctx.Err() != nil)
	cancel()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if finishErr != nil {
		return nil, finishErr
	}
	for err := range errorsCh {
		if err != nil {
			return nil, err
		}
	}
	return store.ReadLiteratureStudyBatch(ctx, owner)
}
