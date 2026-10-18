package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestManualContextCompactionRejectsGenericAdmissionAndFinish(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
	input := map[string]any{"turn_intent": transcriptstore.ContextCompactionIntent, "context_compaction": request}
	before, err := f.repo.GetStream(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	rejected := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", map[string]any{
		"content": "Compact conversation context.", "files": []any{}, "loading_id": "forged-public-control", "turn_intent": transcriptstore.ContextCompactionIntent}, f.stream.OwnerID)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("ordinary message route accepted maintenance: HTTP %d %s", rejected.Code, rejected.Body.String())
	}
	_, _, created, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "forged-control", FrameEventID: "forged-event",
		MessageUUID: "forged-message", Text: "Compact conversation context.", InputData: input})
	if created || !errors.Is(err, transcriptstore.ErrContextCompactionInvalid) {
		t.Fatalf("generic frame admission created=%t err=%v", created, err)
	}
	raw, _ := json.Marshal(map[string]any{"text": "Compact conversation context.", "inputData": input})
	_, created, err = f.repo.AppendUserEvent(context.Background(), transcriptstore.AppendUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "forged-append", PayloadJSON: raw})
	if created || !errors.Is(err, transcriptstore.ErrContextCompactionInvalid) {
		t.Fatalf("generic append created=%t err=%v", created, err)
	}
	after, err := f.repo.GetStream(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if before.InputRevision != after.InputRevision {
		t.Fatal("forged control changed canonical admission")
	}
	appendManualCompactionInputForTest(t, f)
	claimSkillControlInput(t, f)
	raw, _ = json.Marshal(manualCompactionBoundaryForTest(request))
	_, _, _, err = f.repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: "forged-boundary", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw})
	if err != nil {
		t.Fatal(err)
	}
	_, _, created, err = f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{Claim: f.claim,
		ClientMessageID: "generic-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`)})
	if created || !errors.Is(err, transcriptstore.ErrContextCompactionInvalid) {
		t.Fatalf("generic finish created=%t err=%v", created, err)
	}
	receipt, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request)
	if err != nil || receipt.TerminalEventID != 0 || receipt.State == "committed" {
		t.Fatalf("forged completion=%#v err=%v", receipt, err)
	}
}

func TestManualContextCompactionPhaseAloneCannotCropSQLOrMemory(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	old, _, err := f.repo.AppendRunnerEvent(context.Background(), transcriptstore.AppendEventInput{Claim: f.claim, ClientMessageID: "old-detail",
		Type: "assistant_message", Source: transcriptstore.EventSourcePayload, PayloadJSON: []byte(`{"text":"OLD_DETAIL_MUST_REMAIN"}`)})
	if err != nil {
		t.Fatal(err)
	}
	request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
	raw, _ := json.Marshal(manualCompactionBoundaryForTest(request))
	_, fake, _, err := f.repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: "context-compaction-boundary:" + request.RequestID, Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw})
	if err != nil {
		t.Fatal(err)
	}
	finishSkillControlInput(t, f)
	projected, err := f.repo.ListRunnerReplay(context.Background(), transcriptstore.ListRunnerReplayInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, MessageLimit: 100, CheckpointLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range projected {
		if event.Event.EventID == old.EventID {
			found = true
		}
		if event.Event.EventID == fake.EventID && event.CompactionBoundary != nil {
			t.Fatal("unadmitted phase acquired proof")
		}
	}
	if !found {
		t.Fatal("SQL cropped ordinary history from a forged phase")
	}
	message := eventjournal.Message{}
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	message["type"] = "runner_checkpoint"
	entries := []eventjournal.Entry{{EventID: old.EventID, Message: eventjournal.Message{"type": "message", "role": "assistant", "text": "OLD_DETAIL_MUST_REMAIN"}},
		{EventID: fake.EventID, Message: message}}
	if _, _, found := latestCompactModelContext(entries); found {
		t.Fatal("unproven JSON cropped in-memory history")
	}
}

func TestManualContextCompactionBoundaryAndTerminalRollbackTogether(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	appendManualCompactionInputForTest(t, f)
	claimSkillControlInput(t, f)
	control, err := f.repo.ClaimedRunnerInputContext(context.Background(), f.claim)
	if err != nil {
		t.Fatal(err)
	}
	request, err := transcriptstore.ContextCompactionRequestFromPayload(map[string]any{"inputData": control.InputData})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`CREATE TRIGGER reject_manual_terminal BEFORE INSERT ON transcript_events
  WHEN NEW.event_type='runner_finished' BEGIN SELECT RAISE(ABORT,'injected terminal failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.FinishContextCompaction(context.Background(), f.claim, *request, manualCompactionBoundaryForTest(*request)); err == nil {
		t.Fatal("injected terminal failure accepted")
	}
	var count int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND client_message_id IN (?,?)`,
		f.stream.UID, "context-compaction-boundary:"+request.RequestID, "context-compaction-terminal:"+request.RequestID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partial boundary/terminal committed: %d", count)
	}
	if _, err = f.db.Exec("DROP TRIGGER reject_manual_terminal"); err != nil {
		t.Fatal(err)
	}
	first := finishManualCompactionForTest(t, f)
	replay, err := f.repo.FinishContextCompaction(context.Background(), f.claim, *request, manualCompactionBoundaryForTest(*request))
	if err != nil || !replay.Replayed || replay.BoundaryEventID != first.BoundaryEventID || replay.TerminalEventID != first.TerminalEventID {
		t.Fatalf("settlement replay=%#v err=%v", replay, err)
	}
}
