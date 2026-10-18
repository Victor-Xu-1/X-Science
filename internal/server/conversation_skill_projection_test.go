package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestConversationSkillContinuationBoundedHistoryPreservesGoalAndCorrection(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	appendSkillControlInput(t, f, "latest-correction", "LATEST_CORRECTION: preserve unresolved limitations.", false)
	claimSkillControlInput(t, f)
	for index := 0; index < 30; index++ {
		payload, _ := json.Marshal(map[string]any{"text": fmt.Sprintf("Trace %d: %s", index, strings.Repeat("evidence ", 1500))})
		if _, _, err := f.repo.AppendRunnerEvent(context.Background(), transcriptstore.AppendEventInput{
			Claim: f.claim, ClientMessageID: fmt.Sprintf("long-trace-%d", index), Type: "assistant_message",
			Source: transcriptstore.EventSourcePayload, PayloadJSON: payload,
		}); err != nil {
			t.Fatal(err)
		}
	}
	finishSkillControlInput(t, f)
	appendSkillControlInput(t, f, "bounded-control", "Hidden bounded control", true)
	run := claimSkillControlInput(t, f)
	control, err := f.repo.ClaimedRunnerInputContext(context.Background(), f.claim)
	if err != nil || control == nil {
		t.Fatalf("control=%#v err=%v", control, err)
	}
	source, err := f.server.conversationSkillControlEvidence(context.Background(), run.Transcript, *control)
	if err != nil {
		t.Fatal(err)
	}
	if source.Omitted == 0 || len(source.Transcript) > conversationSkillTranscriptMaxBytes {
		t.Fatalf("unbounded or unaccounted history: omitted=%d bytes=%d", source.Omitted, len(source.Transcript))
	}
	for _, required := range []string{"Create scientific artifacts.", "LATEST_CORRECTION", "records omitted"} {
		if !strings.Contains(source.Transcript, required) {
			t.Errorf("bounded history lost %q", required)
		}
	}
}

func TestConversationSkillContinuationHiddenProjectionKeepsCanonicalCoordinates(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	control := appendSkillControlInput(t, f, "hidden-control", "Internal control", true)
	appendSkillControlInput(t, f, "ordinary-after", "Ordinary visible request", false)
	history, snapshot, err := f.server.projectTranscriptWebHistory(context.Background(), f.stream, f.stream.OwnerID, f.stream.SessionID, "")
	if err != nil {
		t.Fatal(err)
	}
	var full map[string]any
	ordinal := 0
	for index, message := range history {
		if message["msg_id"] == "hidden-control" {
			full, ordinal = message, index+1
		}
		if message["msg_id"] == "ordinary-after" && message["hidden"] == true {
			t.Fatal("ordinary input was hidden")
		}
	}
	if full == nil || full["hidden"] != true || full["turn_intent"] != conversationSkillControlIntent || full["id"] != "hidden-control-message" {
		t.Fatalf("full history removed or rewrote canonical control: %#v", full)
	}
	coordinates := full["content"].(map[string]any)["xScience"].(map[string]any)
	visibleIndex := coordinates["messageIndex"].(int)
	events, err := f.repo.ListProjectedCoordinateEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, BranchID: snapshot.BranchID,
		BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	var incremental transcriptstore.TranscriptWebMessageRecord
	for _, event := range events {
		if event.Event.ClientMessageID == "hidden-control" {
			incremental, _, err = buildIncrementalTranscriptWebUserMessage(event, f.stream.SessionID, snapshot.BranchID, ordinal-1, visibleIndex)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	var message map[string]any
	if err := json.Unmarshal(incremental.MessageJSON, &message); err != nil {
		t.Fatal(err)
	}
	if message["hidden"] != true || message["msg_id"] != "hidden-control" || message["id"] != full["id"] ||
		incremental.Ordinal != ordinal || incremental.VisibleIndex == nil || *incremental.VisibleIndex != visibleIndex ||
		incremental.FirstPublicationSequence != control.PublicationSeq || incremental.LastPublicationSequence != control.PublicationSeq {
		t.Fatalf("incremental control changed identity/coordinates: %#v %#v", incremental, message)
	}
	if err := f.server.drainTranscriptWebDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if event.Type == "message.userCreated" && event.Payload["msg_id"] == "hidden-control" {
			found = true
			if event.Payload["hidden"] != true || event.Payload["turn_intent"] != conversationSkillControlIntent ||
				event.ID != fmt.Sprintf("transcript-web:%s:%d:user", f.stream.UID, control.PublicationSeq) {
				t.Fatalf("live control lost canonical identity: %#v", event)
			}
		}
	}
	if !found {
		t.Fatal("hidden control was removed from the realtime projection")
	}
}

func TestConversationSkillContinuationReplaySkipsTypedControlsNotLiteralRequests(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact-%t", compact), func(t *testing.T) {
			entries := []eventjournal.Entry{
				{EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": "Save as skill"}},
				{EventID: 2, Message: eventjournal.Message{"type": "message", "role": "user", "text": "OLD_HIDDEN_CONTROL", "inputData": map[string]any{"turn_intent": conversationSkillControlIntent}}},
			}
			if compact {
				entries = append(entries, eventjournal.Entry{EventID: 3, Message: eventjournal.Message{"type": "runner_checkpoint", "toolPhase": "auto_compact", "summary": "Recorded ordinary work."}})
			}
			entries = append(entries,
				eventjournal.Entry{EventID: 4, Message: eventjournal.Message{"type": "message", "role": "assistant", "text": "Ordinary visible result"}},
				eventjournal.Entry{EventID: 5, Message: eventjournal.Message{"type": "message", "role": "user", "text": "CURRENT_HIDDEN_CONTROL", "inputData": map[string]any{"turn_intent": conversationSkillControlIntent}}})
			messages := requireProviderReplayMessages(t, "Current assistant", entries)
			userCount, assistantCount := 0, 0
			for _, message := range messages {
				if strings.Contains(message.Content, "HIDDEN_CONTROL") {
					t.Fatal("control became a recursive model input")
				}
				if message.Role == "user" && message.Content == "Save as skill" {
					userCount++
				}
				if message.Role == "assistant" && message.Content == "Ordinary visible result" {
					assistantCount++
				}
			}
			if userCount != 1 || assistantCount != 1 {
				t.Fatalf("ordinary replay changed: %#v", messages)
			}
		})
	}
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	appendSkillControlInput(t, f, "literal-save", "Save as skill", false)
	run := claimSkillControlInput(t, f)
	options, err := f.server.prepareConversationSkillContinuation(context.Background(), SessionRunnerChatOptions{SystemPrompt: "ordinary"}, run)
	if err != nil || options.SystemPrompt != "ordinary" {
		t.Fatalf("untyped literal invoked Skill authoring: options=%#v err=%v", options, err)
	}
}
