package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Server) buildCanonicalCompactionDetails(session sessionstore.Session, entries []eventjournal.Entry, instructions, trigger string) (map[string]any, error) {
	summary, err := buildCompactModelContextSummary(session, entries, instructions, trigger)
	if err != nil {
		return nil, err
	}
	summary = s.compactSummaryWithPlanNavigation(session.ID, summary)
	continuity, err := sessionRunnerToolContinuityRecords(entries)
	if err != nil {
		return nil, err
	}
	modelContext := map[string]any{"summary": summary}
	addCompactPublicHandoff(modelContext, entries)
	reason := "canonical manual compaction uses durable deterministic context"
	if trigger == "auto" {
		reason = "automatic runner compaction uses durable deterministic context"
	}
	details := map[string]any{"summary": summary, "modelContext": modelContext, "summaryStatus": "deterministic-context",
		"modelSummary": map[string]any{"attempted": false, "provider": "deterministic", "reason": reason},
		"trigger":      trigger, "compactedThroughEventId": maxEntryIDFromEntries(entries)}
	if len(continuity) > 0 {
		details[sessionRunnerToolContinuityField] = continuity
	}
	return details, nil
}

// The source is a frozen branch/publication read, not the newest input or a
// legacy journal. Bounds fail visibly rather than silently truncate authority.
func (s *Server) contextCompactionSource(ctx context.Context, request transcriptstore.ContextCompactionRequest, owner string) ([]eventjournal.Entry, error) {
	target := request.Expected
	entries := []eventjournal.Entry{}
	var after, bytes int64
	for after < target.ThroughPublicationSeq {
		page, err := s.transcriptStore.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{StreamUID: target.StreamUID, OwnerID: owner,
			BranchID: target.BranchID, BranchGeneration: target.BranchGeneration, AfterPublicationSequence: after,
			ThroughPublicationSequence: target.ThroughPublicationSeq, Limit: 1000})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return nil, errors.New("compaction source snapshot is incomplete")
		}
		for _, projected := range page {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			after = projected.Event.PublicationSeq
			bytes += int64(len(projected.ResolvedPayloadJSON))
			if bytes > 256<<20 || len(entries) >= 100000 {
				return nil, errors.New("compaction source exceeds bounded replay")
			}
			var message eventjournal.Message
			if err := json.Unmarshal(projected.ResolvedPayloadJSON, &message); err != nil {
				return nil, err
			}
			if isHiddenConversationControlPayload(message) {
				continue
			}
			switch projected.Event.Type {
			case "user_message", "user_input_response", "history_user_message":
				message["type"], message["role"] = "message", "user"
			case "assistant_message", "history_assistant_message":
				message["type"], message["role"] = "message", "assistant"
			case "history_system_message":
				message["type"], message["role"] = "message", "system"
			case "runner_checkpoint", transcriptstore.TerminalToolRecoveryEventType:
				message["type"] = "runner_checkpoint"
				if message["purpose"] == transcriptstore.ContextCompactionPurpose && !transcriptstore.IsCompactionBoundaryPayload(message) {
					continue
				}
			default:
				continue
			}
			if projected.Event.RunnerAttempt != nil {
				message["runnerAttempt"] = *projected.Event.RunnerAttempt
			}
			var provenance any
			if message["toolPhase"] == transcriptstore.ContextCompactionPhase {
				proof, err := s.transcriptStore.ContextCompactionBoundaryProof(ctx, owner, target.StreamUID, projected.Event.EventID)
				if err != nil {
					return nil, err
				}
				if proof != nil {
					provenance = *proof
				}
			}
			entries = append(entries, eventjournal.Entry{SessionID: target.StreamUID, EventID: projected.Event.EventID,
				CreatedAt: projected.Event.CreatedAt.UTC().Format(time.RFC3339Nano), ClientMessageID: projected.Event.ClientMessageID,
				Message: message, SourceEventType: projected.Event.Type, RuntimeProjection: provenance})
		}
	}
	return entries, nil
}
