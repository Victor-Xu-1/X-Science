package server

import (
	"context"
	"encoding/json"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func typedManualCompactionRequestForTest(t *testing.T, f *agentSaveArtifactsFixture, id string) transcriptstore.ContextCompactionRequest {
	t.Helper()
	raw, err := json.Marshal(manualContextCompactionRequestForTest(t, f, id))
	if err != nil {
		t.Fatal(err)
	}
	var request transcriptstore.ContextCompactionRequest
	if err = json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func appendManualCompactionInputForTest(t *testing.T, f *agentSaveArtifactsFixture) transcriptstore.Event {
	t.Helper()
	request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
	receipt, err := f.store.AdmitContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request)
	if err != nil || !receipt.Accepted {
		t.Fatalf("admit actual control=%#v err=%v", receipt, err)
	}
	f.stream, err = f.repo.GetStream(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := f.repo.ListProjectedEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Event.ClientMessageID == receipt.InputMessageID {
			return event.Event
		}
	}
	t.Fatal("real admitted control missing")
	return transcriptstore.Event{}
}

func finishManualCompactionForTest(t *testing.T, f *agentSaveArtifactsFixture) transcriptstore.ContextCompactionReceipt {
	t.Helper()
	control, err := f.repo.ClaimedRunnerInputContext(context.Background(), f.claim)
	if err != nil || control == nil {
		t.Fatalf("claimed control=%#v err=%v", control, err)
	}
	request, err := transcriptstore.ContextCompactionRequestFromPayload(map[string]any{"inputData": control.InputData})
	if err != nil || request == nil {
		t.Fatalf("request=%#v err=%v", request, err)
	}
	receipt, err := f.repo.FinishContextCompaction(context.Background(), f.claim, *request, manualCompactionBoundaryForTest(*request))
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func manualCompactionBoundaryForTest(request transcriptstore.ContextCompactionRequest) map[string]any {
	return map[string]any{"status": "completed", "toolPhase": transcriptstore.ContextCompactionPhase,
		"purpose": transcriptstore.ContextCompactionPurpose, "maintenance": request, "summary": "Preserved verified evidence.",
		"compaction": map[string]any{"schema": transcriptstore.ContextCompactionBoundarySchema, "reason": "manual", "request_id": request.RequestID}}
}
