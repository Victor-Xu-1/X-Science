package server

import (
	"context"
	"fmt"
	"runtime/debug"

	"x-science/internal/buildinfo"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// Diagnostic lookup only: eligibility stays with the recovery scan. A raced
// or historical coordinate is explicitly unknown, never rebound to "latest".
func (s *Server) frameResumeCandidateWakeCause(ctx context.Context, candidate transcriptstore.AutoResumeCandidate) workspace.FrameResumeWakeCause {
	cause := workspace.FrameResumeWakeCause{Kind: "auto_resume_checkpoint", Evidence: "unknown",
		StreamUID: candidate.StreamUID, RunnerAttempt: candidate.Attempt, CheckpointSequence: candidate.CheckpointSequence}
	checkpoint, found, err := s.transcriptStore.GetResumableCheckpoint(ctx, candidate.StreamUID, candidate.OwnerID, candidate.Attempt, candidate.CheckpointSequence)
	if err == nil && found {
		cause.EventID, cause.Evidence = checkpoint.EventID, "canonical"
		cause.ReferenceID = fmt.Sprintf("%s:%d", checkpoint.StreamUID, checkpoint.EventID)
	}
	return cause
}

func frameResumeRecoveryContractWakeCause(previous int) workspace.FrameResumeWakeCause {
	cause := workspace.FrameResumeWakeCause{Kind: "recovery_contract_revision", Evidence: "observed",
		ReferenceID:      fmt.Sprintf("recovery-contract:%d", sessionRunnerRecoveryContractRevision),
		PreviousRevision: int64(previous), Revision: int64(sessionRunnerRecoveryContractRevision), RuntimeVersion: buildinfo.Release().Version}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				cause.RuntimeRevision = setting.Value
			}
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				cause.RuntimeDirty = true
			}
		}
	}
	return cause
}
