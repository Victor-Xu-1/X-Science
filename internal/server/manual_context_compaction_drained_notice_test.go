package server

import (
	"context"
	"net/http"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualContextCompactionOwnerCleanupInvalidatesDrainingReceipt(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	control := appendManualCompactionInputForTest(t, f)
	run := claimSkillControlInput(t, f)
	runCtx, active, finish, err := f.server.registerActiveSessionRun(context.Background(), f.stream.FrameID, f.claim.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	active.transcript = run.Transcript
	handled, receipt, err := f.server.runContextCompaction(runCtx, sessionstore.Session{ID: f.stream.FrameID}, run, active)
	if err != nil || !handled || receipt.State != "committed" {
		t.Fatalf("real maintenance handled=%t receipt=%#v err=%v", handled, receipt, err)
	}
	if err := f.server.drainTranscriptWebDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + f.stream.FrameID + "/context-compactions"
	response := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot HTTP %d: %s", response.Code, response.Body.String())
	}
	operation := p3DecodeObject(t, response)["operation"].(map[string]any)
	if operation["execution_observation"] != "draining" || operation["can_submit_next_input"] != false {
		t.Fatalf("terminal publication skipped live owner: %#v", operation)
	}
	before := map[string]bool{}
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		before[event.ID] = true
	}
	finish()
	found := 0
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if before[event.ID] || event.Type != "compaction_status" {
			continue
		}
		if event.FrameID != f.stream.FrameID || event.UserID != f.stream.OwnerID ||
			event.Payload["request_id"] != receipt.Request.RequestID ||
			event.Payload["frame_incarnation_id"] != receipt.Request.Expected.FrameIncarnationID ||
			event.Payload["root_frame_incarnation_id"] != receipt.Request.Expected.RootFrameIncarnationID ||
			event.Payload["state"] != "committed" {
			t.Fatalf("cleanup notice borrowed another owner: %#v", event)
		}
		found++
	}
	if found != 1 {
		t.Fatalf("owner cleanup must invalidate the already-delivered draining receipt once; got %d, input=%s", found, control.ClientMessageID)
	}
	finish()
	response = p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID)
	operation = p3DecodeObject(t, response)["operation"].(map[string]any)
	if operation["can_submit_next_input"] != true || operation["execution_observation"] != "unavailable" {
		t.Fatalf("cleanup notice invented physical status or retained the gate: %#v", operation)
	}
}

func TestManualContextCompactionDrainedNoticeRespectsCancelledContext(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	before := len(transcriptWebEvents(t, f.store, f.stream.OwnerID))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.server.publishCompatEventContext(ctx, workspace.RealtimeEventInput{
		ID: "cancelled-drain-notice", UserID: f.stream.OwnerID, ProjectID: f.stream.ProjectID,
		RootFrameID: f.stream.RootFrameID, FrameID: f.stream.FrameID, Type: "compaction_status",
		Payload: map[string]any{"schema": "x-science.context-compaction-event.v1", "conversation_id": f.stream.FrameID},
	})
	if err == nil {
		t.Fatal("cancelled notification context unexpectedly committed")
	}
	if after := len(transcriptWebEvents(t, f.store, f.stream.OwnerID)); after != before {
		t.Fatalf("cancelled notification persisted a new event: before=%d after=%d", before, after)
	}
}
