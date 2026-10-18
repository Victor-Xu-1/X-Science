package server

import (
	"context"
	"net/http"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestAsideCancellationTargetsAcceptedInputAndPreservesData(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	const intent = "b5037ebc-9ee0-4a08-9ff0-a2fb10d2ba20"
	accepted := p3JSONRequest(t, f.server, http.MethodPost, "/api/frames/"+f.stream.FrameID+"/aside", map[string]any{
		"request": "Independent child task", "intent_id": intent,
	}, f.stream.OwnerID)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", accepted.Code, accepted.Body.String())
	}
	child := webString(p3DecodeObject(t, accepted)["frame_id"])
	path := "/api/frames/" + child + "/cancel"
	for _, invalid := range []any{nil, "", 42} {
		response := p3JSONRequest(t, f.server, http.MethodPost, path, map[string]any{"expected_input_message_id": invalid}, f.stream.OwnerID)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid target: %d %s", response.Code, response.Body.String())
		}
	}
	wrong := p3JSONRequest(t, f.server, http.MethodPost, path, map[string]any{"expected_input_message_id": "other-input"}, f.stream.OwnerID)
	if wrong.Code != http.StatusConflict || p3DecodeObject(t, wrong)["code"] != "CONVERSATION_CANCEL_INPUT_STALE" {
		t.Fatalf("wrong target: %d %s", wrong.Code, wrong.Body.String())
	}
	for repeat := 0; repeat < 2; repeat++ {
		cancel := p3JSONRequest(t, f.server, http.MethodPost, path, map[string]any{"expected_input_message_id": intent}, f.stream.OwnerID)
		if cancel.Code != http.StatusOK || p3DecodeObject(t, cancel)["root_frame_id"] != child {
			t.Fatalf("cancel: %d %s", cancel.Code, cancel.Body.String())
		}
	}
	parent, found, err := f.store.GetFrame(f.stream.FrameID)
	if err != nil || !found || parent.Status != "completed" {
		t.Fatalf("parent altered: %#v %v", parent, err)
	}
	stream, found, err := f.repo.GetFrameStreamBySession(context.Background(), f.stream.OwnerID, child)
	if err != nil || !found {
		t.Fatalf("child lost: %v", err)
	}
	if _, _, _, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "child-followup", FrameEventID: "child-followup-event", MessageUUID: "child-followup-message", Text: "Continue the same child",
	}); err != nil {
		t.Fatal(err)
	}
	late := p3JSONRequest(t, f.server, http.MethodPost, path, map[string]any{"expected_input_message_id": intent}, f.stream.OwnerID)
	if late.Code != http.StatusConflict {
		t.Fatalf("late cancel accepted: %d %s", late.Code, late.Body.String())
	}
	frame, found, err := f.store.GetFrame(child)
	if err != nil || !found || frame.Status != "processing" {
		t.Fatalf("new followup stopped: %#v %v", frame, err)
	}
	read := p3JSONRequest(t, f.server, http.MethodGet, "/api/frames/"+f.stream.FrameID+"/aside?intent_id="+intent, nil, f.stream.OwnerID)
	if read.Code != http.StatusOK || p3DecodeObject(t, read)["frame_id"] != child {
		t.Fatalf("acceptance lost after cancellation: %s", read.Body.String())
	}
	legacy := p3JSONRequest(t, f.server, http.MethodPost, path, map[string]any{}, f.stream.OwnerID)
	if legacy.Code != http.StatusOK {
		t.Fatalf("old cancel contract changed: %d %s", legacy.Code, legacy.Body.String())
	}
	var inputs int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='user_message'`, stream.UID).Scan(&inputs); err != nil || inputs != 2 {
		t.Fatalf("canonical data lost: inputs=%d %v", inputs, err)
	}
}
