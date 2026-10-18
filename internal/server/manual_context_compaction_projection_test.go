package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

const manualCompactionIntentForTest = "compact-context"

func TestManualContextCompactionControlProjectionKeepsCanonicalCoordinates(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	control := appendManualCompactionInputForTest(t, f)
	goal, foundGoal, goalErr := f.repo.GetActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID)
	if goalErr != nil || !foundGoal || goal.Text != "Create scientific artifacts." {
		t.Fatalf("maintenance replaced canonical goal: %#v found=%t err=%v", goal, foundGoal, goalErr)
	}
	appendSkillControlInput(t, f, "ordinary-after-maintenance", "Keep the original work visible.", false)
	history, snapshot, err := f.server.projectTranscriptWebHistory(context.Background(), f.stream, f.stream.OwnerID, f.stream.SessionID, "")
	if err != nil {
		t.Fatal(err)
	}
	var full map[string]any
	ordinal := 0
	for index, message := range history {
		if message["msg_id"] == control.ClientMessageID {
			full, ordinal = message, index+1
		}
		if message["msg_id"] == "ordinary-after-maintenance" && message["hidden"] == true {
			t.Error("ordinary message was hidden")
		}
	}
	if full == nil {
		t.Fatal("canonical control disappeared instead of being hidden")
	}
	if full["hidden"] != true || full["turn_intent"] != manualCompactionIntentForTest {
		t.Errorf("full control is visible: %#v", full)
	}
	coordinates := full["content"].(map[string]any)["xScience"].(map[string]any)
	visibleIndex := coordinates["messageIndex"].(int)
	events, err := f.repo.ListProjectedCoordinateEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
		ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Event.ClientMessageID != control.ClientMessageID {
			continue
		}
		record, _, err := buildIncrementalTranscriptWebUserMessage(event, f.stream.SessionID, snapshot.BranchID, ordinal-1, visibleIndex)
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err := json.Unmarshal(record.MessageJSON, &message); err != nil {
			t.Fatal(err)
		}
		if message["hidden"] != true || message["id"] != full["id"] || record.Ordinal != ordinal || record.VisibleIndex == nil ||
			*record.VisibleIndex != visibleIndex || record.FirstPublicationSequence != control.PublicationSeq || record.LastPublicationSequence != control.PublicationSeq {
			t.Errorf("incremental control lost hidden coordinates: %#v %#v", record, message)
		}
	}
	if err := f.server.drainTranscriptWebDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if event.Type == "message.userCreated" && event.Payload["msg_id"] == control.ClientMessageID {
			found = true
			if event.Payload["hidden"] != true || event.Payload["turn_intent"] != manualCompactionIntentForTest {
				t.Errorf("realtime control is visible: %#v", event)
			}
		}
	}
	if !found {
		t.Fatal("canonical realtime control disappeared")
	}
}

func TestManualContextCompactionControlExcludedFromProviderAndSkillEvidence(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		entries := []eventjournal.Entry{
			{EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": "Please compact context after explaining the data."}},
			{EventID: 2, Message: eventjournal.Message{"type": "message", "role": "user", "text": "INTERNAL_COMPACTION_CONTROL", "inputData": map[string]any{"turn_intent": manualCompactionIntentForTest}}},
		}
		messages := requireProviderReplayMessages(t, "ordinary assistant", entries)
		literalFound := false
		for _, message := range messages {
			if strings.Contains(message.Content, "INTERNAL_COMPACTION_CONTROL") {
				t.Error("maintenance was replayed as a user goal")
			}
			literalFound = literalFound || message.Content == "Please compact context after explaining the data."
		}
		if !literalFound {
			t.Fatal("literal ordinary request was filtered")
		}
	})
	t.Run("skill-evidence", func(t *testing.T) {
		f := newAgentSaveArtifactsFixture(t)
		finishSkillControlInput(t, f)
		input := appendManualCompactionInputForTest(t, f)
		var inputPayload map[string]any
		if err := json.Unmarshal(input.PayloadJSON, &inputPayload); err != nil {
			t.Fatal(err)
		}
		controlText := stringValue(inputPayload["text"])
		if controlText == "" {
			t.Fatal("real server-generated control text missing")
		}
		claimSkillControlInput(t, f)
		finishManualCompactionForTest(t, f)
		appendSkillControlInput(t, f, "save-after-maintenance", "Save current evidence", true)
		run := claimSkillControlInput(t, f)
		control, err := f.repo.ClaimedRunnerInputContext(context.Background(), f.claim)
		if err != nil || control == nil {
			t.Fatalf("actual Skill control=%#v err=%v", control, err)
		}
		source, err := f.server.conversationSkillControlEvidence(context.Background(), run.Transcript, *control)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(source.Transcript, controlText) {
			t.Error("maintenance entered saved Skill source")
		}
		if !strings.Contains(source.Transcript, "Create scientific artifacts.") {
			t.Fatal("original goal was lost")
		}
	})
}

func TestManualContextCompactionMemoryCropUsesCanonicalBoundary(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	appendManualCompactionInputForTest(t, f)
	claimSkillControlInput(t, f)
	receipt := finishManualCompactionForTest(t, f)
	proof, err := f.repo.ContextCompactionBoundaryProof(context.Background(), f.stream.OwnerID, f.stream.UID, receipt.BoundaryEventID)
	if err != nil || proof == nil {
		t.Fatalf("canonical proof=%#v err=%v", proof, err)
	}
	entries := []eventjournal.Entry{
		{EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": "Keep original requirements."}},
		{EventID: 2, Message: eventjournal.Message{"type": "message", "role": "assistant", "text": "OLD_ASSISTANT_DETAIL"}},
		{EventID: receipt.BoundaryEventID, RuntimeProjection: *proof, Message: eventjournal.Message{"type": "runner_checkpoint", "status": "completed", "toolPhase": "context_compact", "summary": "Preserved verified evidence.",
			"compaction": map[string]any{"schema": "x-science.context-compaction-boundary.v1", "reason": "manual", "request_id": "c9476bfa-0901-4ba1-abf7-07f2ce9314d5"}}},
	}
	summary, boundary, found := latestCompactModelContext(entries)
	if !found || boundary != receipt.BoundaryEventID || summary != "Preserved verified evidence." {
		t.Fatalf("manual memory boundary missing: found=%t id=%d summary=%q", found, boundary, summary)
	}
	for _, message := range requireProviderReplayMessages(t, "ordinary assistant", entries) {
		if strings.Contains(message.Content, "OLD_ASSISTANT_DETAIL") {
			t.Fatal("memory crop replayed pre-boundary assistant history")
		}
	}
}
