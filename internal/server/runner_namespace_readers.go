package server

import (
	"context"
	"encoding/json"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Server) parseRunnerNamespaceCause(ctx context.Context, ownerID string, event transcriptstore.Event, message eventjournal.Message) (transcriptstore.RunnerInterruptionCause, bool, error) {
	cause, present, err := transcriptstore.ParseRunnerInterruptionCause(message)
	if err == nil || s == nil || s.transcriptStore == nil {
		return cause, present, err
	}
	historical, proofErr := s.transcriptStore.IsHistoricalNamespaceEvent(ctx, ownerID, event)
	if proofErr != nil {
		return cause, present, proofErr
	}
	if !historical {
		return cause, present, err
	}
	var original map[string]any
	if decodeErr := json.Unmarshal(event.PayloadJSON, &original); decodeErr != nil {
		return cause, present, decodeErr
	}
	return transcriptstore.ParseHistoricalRunnerInterruptionCause(original)
}

func (s *Server) observeNamespaceCorrectionChunk(ctx context.Context, ownerID string, event transcriptstore.Event, message eventjournal.Message, assembler *transcriptstore.RunnerCorrectionAssembler) (bool, error) {
	if _, present := message[transcriptstore.RunnerCorrectionConditionChunkField]; !present {
		return assembler.Observe(message)
	}
	if s == nil || s.transcriptStore == nil {
		return assembler.Observe(message)
	}
	historical, err := s.transcriptStore.IsHistoricalNamespaceEvent(ctx, ownerID, event)
	if err != nil {
		return true, err
	}
	if historical {
		var original eventjournal.Message
		if err := json.Unmarshal(event.PayloadJSON, &original); err != nil {
			return true, err
		}
		return assembler.ObserveHistorical(original)
	}
	return assembler.Observe(message)
}

// Only a copied presentation payload is canonicalized. The original Event and
// its bytes remain unchanged for source-chain hashing, recovery, and audit.
func (s *Server) normalizeNamespaceAskUserProjection(ctx context.Context, ownerID string, projected transcriptstore.ProjectedEvent) (transcriptstore.ProjectedEvent, error) {
	if projected.Event.Type != transcriptstore.AskUserPromptEventType || projected.Event.Source != transcriptstore.EventSourcePayload {
		return projected, nil
	}
	if _, err := transcriptstore.DecodeAskUserPromptV1(projected.ResolvedPayloadJSON); err == nil {
		return projected, nil
	} else {
		if s == nil || s.transcriptStore == nil {
			return projected, err
		}
		historical, proofErr := s.transcriptStore.IsHistoricalNamespaceEvent(ctx, ownerID, projected.Event)
		if proofErr != nil {
			return projected, proofErr
		}
		if !historical {
			return projected, err
		}
	}
	prompt, err := transcriptstore.DecodeHistoricalAskUserPromptV1(projected.Event.PayloadJSON)
	if err != nil {
		return projected, err
	}
	projected.ResolvedPayloadJSON, err = json.Marshal(prompt)
	return projected, err
}

func (s *Server) normalizeNamespaceAskUserProjectionEvents(ctx context.Context, ownerID string, events []transcriptstore.ProjectedEvent) ([]transcriptstore.ProjectedEvent, error) {
	for index, projected := range events {
		value, err := s.normalizeNamespaceAskUserProjection(ctx, ownerID, projected)
		if err != nil {
			return nil, err
		}
		events[index] = value
	}
	return events, nil
}
