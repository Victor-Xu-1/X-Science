package server

import (
	"context"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

// projectClaimedRunnerInputData is an ephemeral runtime projection, not another
// persisted input authority. Every profile/tool binding for this attempt sees
// the same claimed composer data instead of a stale JSON-session cache.
func (s *Server) projectClaimedRunnerInputData(
	ctx context.Context, session sessionstore.Session, authority *transcriptRunnerAuthority,
) (sessionstore.Session, error) {
	if authority == nil {
		return session, nil
	}
	if authority.Stream.SessionID != session.ID {
		return sessionstore.Session{}, transcriptstore.ErrOwnerMismatch
	}
	inputData, err := s.transcriptStore.ClaimedRunnerInputData(ctx, authority.Claim)
	if err != nil {
		return sessionstore.Session{}, err
	}
	orchestration := copyMapAny(session.Orchestration)
	if orchestration == nil {
		orchestration = map[string]any{}
	}
	delete(orchestration, "inputData")
	if len(inputData) > 0 {
		orchestration["inputData"] = inputData
	}
	session.Orchestration = orchestration
	return session, nil
}

func (s *Server) runtimeComposerInputData(ctx context.Context, frameID string) (map[string]any, error) {
	if source, ok := ctx.Value(specialistExecutionSourceContextKey{}).(specialistExecutionSource); ok {
		if _, err := s.claimedSessionSpecialistForFrame(ctx, source.OwnerID, frameID); err != nil {
			return nil, err
		}
		return copyMapAny(source.Source.InputData), nil
	}
	if run, found := transcriptRunnerChatRunFromContext(ctx); found {
		if run.Transcript.Stream.SessionID != frameID {
			return nil, transcriptstore.ErrOwnerMismatch
		}
		return s.transcriptStore.ClaimedRunnerInputData(ctx, run.Transcript.Claim)
	}
	if claim, found := kernelTranscriptClaimFromContext(ctx); found {
		stream, err := s.transcriptStore.ValidateLiveRunnerClaim(ctx, claim)
		if err != nil {
			return nil, err
		}
		if stream.FrameID != frameID {
			return nil, transcriptstore.ErrOwnerMismatch
		}
		return s.transcriptStore.ClaimedRunnerInputData(ctx, claim)
	}
	if _, canonical, err := s.resolveTranscriptFrameStream(ctx, frameID); err != nil || canonical {
		// Catalog/UI discovery has no claimed turn. Never borrow an old session's
		// selection for a canonical Frame merely because no runner was provided.
		return nil, err
	}
	if s.sessionStore == nil {
		return nil, nil
	}
	session, found, err := s.sessionStore.Get(frameID)
	if err != nil || !found {
		return nil, err
	}
	inputData, _ := session.Orchestration["inputData"].(map[string]any)
	return inputData, nil
}
