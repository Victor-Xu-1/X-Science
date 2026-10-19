package server

import (
	"context"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

type specialistExecutionSourceContextKey struct{}
type kernelTranscriptClaimContextKey struct{}

// A transient projection of the original operation, never a new run, claim,
// persisted session spec or mutable frame authority. A nil Snapshot is an
// explicit legacy source and must not inherit the newer claim's selection.
type specialistExecutionSource struct {
	OwnerID, FrameID string
	Claim            transcriptstore.RunnerClaim
	Source           transcriptstore.RunnerAgentSource
}

func (s *Server) claimedSessionSpecialistForFrame(ctx context.Context, owner, frameID string) (*sessionagent.Snapshot, error) {
	if s == nil || ctx == nil {
		return nil, sessionagent.ErrUnavailable
	}
	if source, ok := ctx.Value(specialistExecutionSourceContextKey{}).(specialistExecutionSource); ok {
		if s.transcriptStore == nil {
			return nil, sessionagent.ErrUnavailable
		}
		if source.OwnerID != owner || source.FrameID != frameID {
			return nil, transcriptstore.ErrOwnerMismatch
		}
		stream, err := s.transcriptStore.ValidateLiveRunnerClaim(ctx, source.Claim)
		if err != nil {
			return nil, err
		}
		if stream.FrameID != frameID || stream.OwnerID != owner {
			return nil, transcriptstore.ErrOwnerMismatch
		}
		if source.Source.Unavailable {
			return nil, sessionagent.ErrTargetUnavailable
		}
		return source.Source.Snapshot, nil
	}
	claim, found := kernelTranscriptClaimFromContext(ctx)
	if !found {
		return nil, nil
	}
	if s == nil || s.transcriptStore == nil {
		return nil, sessionagent.ErrUnavailable
	}
	if claim.OwnerID != owner {
		return nil, transcriptstore.ErrOwnerMismatch
	}
	return s.transcriptStore.ClaimedRunnerAgentSnapshotForFrame(ctx, claim, frameID)
}

func kernelTranscriptClaimFromContext(ctx context.Context) (transcriptstore.RunnerClaim, bool) {
	if run, found := transcriptRunnerChatRunFromContext(ctx); found {
		return run.Transcript.Claim, true
	}
	claim, found := ctx.Value(kernelTranscriptClaimContextKey{}).(transcriptstore.RunnerClaim)
	return claim, found && claim.StreamUID != ""
}

func (s *Server) specialistOperationContext(ctx context.Context, run *sessionRunnerChatRun, operation workspace.KernelLocalOperation) (context.Context, *sessionagent.Snapshot, error) {
	if run == nil || run.Transcript == nil || operation.OwnerUserID != run.Transcript.Stream.OwnerID || operation.FrameID != run.Transcript.Stream.FrameID {
		return nil, nil, transcriptstore.ErrOwnerMismatch
	}
	source, err := s.transcriptStore.ClaimedRunnerAgentOperationSource(ctx, run.Transcript.Claim, transcriptstore.AgentOperationSource{
		StreamUID: operation.StreamUID, FrameID: operation.FrameID, EventID: operation.SourceEventID, Attempt: operation.SourceRunnerAttempt,
	})
	if err != nil {
		return nil, nil, err
	}
	if !source.Managed {
		return ctx, nil, nil
	}
	if source.Unavailable {
		return nil, nil, sessionagent.ErrTargetUnavailable
	}
	return context.WithValue(ctx, specialistExecutionSourceContextKey{}, specialistExecutionSource{
		OwnerID: operation.OwnerUserID, FrameID: operation.FrameID, Claim: run.Transcript.Claim, Source: source,
	}), source.Snapshot, nil
}

func (s *Server) specialistRecoveryAuthority(ctx context.Context, run *sessionRunnerChatRun, operation workspace.KernelLocalOperation, current SessionRunnerChatOptions, bases ...SessionRunnerChatOptions) (context.Context, SessionRunnerChatOptions, error) {
	projected, snapshot, err := s.specialistOperationContext(ctx, run, operation)
	if err != nil {
		return nil, current, err
	}
	if _, managed := projected.Value(specialistExecutionSourceContextKey{}).(specialistExecutionSource); !managed {
		return projected, current, nil
	}
	base := current
	if len(bases) > 0 {
		base = bases[0]
	}
	base.agentSnapshot = snapshot
	session, err := s.loadTranscriptFrameSessionProjection(run.Transcript.Stream)
	if err != nil {
		return nil, current, err
	}
	input, err := s.runtimeComposerInputData(projected, operation.FrameID)
	if err != nil {
		return nil, current, err
	}
	session.Orchestration = copyMapAny(session.Orchestration)
	if session.Orchestration == nil {
		session.Orchestration = make(map[string]any)
	}
	delete(session.Orchestration, "inputData")
	if len(input) > 0 {
		session.Orchestration["inputData"] = input
	}
	bound, err := s.bindSessionRunnerToolAuthority(withTranscriptRunnerChatRun(projected, run), session, base)
	if err != nil {
		return nil, current, err
	}
	return projected, bound.Options, nil
}
