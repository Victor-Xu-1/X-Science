package server

import (
	"context"
	"encoding/json"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Historical partial/publication fixtures use the production Tx assistant core
// without a terminal. This is deliberately not a second production finalizer.
func (s *Server) appendTranscriptAssistantEvent(ctx context.Context, authority *transcriptRunnerAuthority, identity string, payload map[string]any) error {
	if authority == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	err = s.appendTranscriptAssistantEventWithTurnArtifacts(ctx, authority, transcriptstore.AppendEventInput{Claim: authority.Claim,
		ClientMessageID: transcriptRunnerClientMessageID(authority.Claim, identity), Type: "assistant_message", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: raw, Destinations: transcriptRunnerDestinations(authority)})
	if err == nil {
		s.signalTranscriptWebDelivery()
	}
	return err
}

func (s *Server) appendTranscriptAssistantEventWithTurnArtifacts(ctx context.Context, authority *transcriptRunnerAuthority, input transcriptstore.AppendEventInput) error {
	return s.transcriptStore.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		_, _, err := tx.AppendFinalAssistant(ctx, input)
		return err
	})
}

func assistantMessageArtifactReferences(snapshot transcriptstore.ArtifactCommitSnapshot, attempt int64, payload []byte) ([]transcriptstore.ArtifactReferenceInput, error) {
	return transcriptstore.FinalAssistantArtifactReferences(snapshot, attempt, payload)
}

// Exercise the real preparation plus the real low-level atomic consumer while
// preserving the pre-existing direct fixture assertions and replay loops.
func consumeCompletionRecoveryFixture(ctx context.Context, s *Server, session sessionstore.Session, run *sessionRunnerChatRun) (string, string, transcriptstore.AssistantSegmentV1, bool, error) {
	final, handled, err := s.recoverTranscriptCompletionOnly(ctx, session, run)
	segment := transcriptstore.AssistantSegmentV1{Ordinal: final.SegmentOrdinal}
	if err != nil || !handled {
		return final.Status, final.Detail, segment, handled, err
	}
	receipt, err := s.persistTranscriptRunnerFinalization(ctx, run.Transcript, final)
	return receipt.Finish.Status, final.Detail, segment, true, err
}
