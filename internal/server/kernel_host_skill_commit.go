package server

import (
	"context"

	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) kernelSkillMutationCommit(ctx context.Context, bound kernelHostExecutionIdentity, access workspace.KernelFrameAccess) (skillDraftCommit, error) {
	identity, err := s.validateKernelTranscriptExecution(ctx, bound, access)
	if err != nil {
		return nil, err
	}
	return func(commit func() error) error {
		// Workspace and Transcript can share a single-connection pool. Verify
		// the frame before borrowing it, never recursively acquire it in the TX.
		if _, err := s.validateKernelHostIdentity(ctx, bound.access); err != nil {
			return err
		}
		outcome, err := s.transcriptStore.RunImmediateWithOutcome(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			live, err := tx.ValidateLiveRunnerClaim(ctx, identity.RunnerClaim)
			if err != nil || live.UID != identity.StreamUID || live.Epoch != identity.StreamEpoch ||
				live.OwnerID != identity.OwnerID || live.ProjectID != identity.ProjectID ||
				live.FrameID != identity.FrameID || live.RootFrameID != identity.RootFrameID ||
				bound.transcriptExecution.currentKernelGeneration() != identity.KernelGeneration {
				return kernelruntime.NewHostCallError("permission_denied", "Skill mutation execution authority is stale")
			}
			// Keep BEGIN IMMEDIATE held through the short filesystem commit.
			// A different runner claim cannot be admitted between check and write.
			return commit()
		})
		if err != nil && outcome.CommitAttempted {
			return kernelruntime.NewHostCallError("outcome_unknown", "Skill publication completed but the execution transaction acknowledgement is uncertain; inspect installed Skills before retrying")
		}
		return err
	}, nil
}
