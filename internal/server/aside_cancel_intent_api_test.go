package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestAsideCancelIntentBeforeAcceptanceSurvivesRetryAndRestart(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	const intent = "6d27b588-4d5c-43d1-8772-594275de122c"
	path := "/api/frames/" + f.stream.FrameID + "/aside"
	query := path + "?intent_id=" + intent
	body := map[string]any{"request": "Cancelled before admission", "intent_id": intent, "as_session": false}
	for repeat := 0; repeat < 2; repeat++ {
		response := p3JSONRequest(t, f.server, http.MethodDelete, query, body, f.stream.OwnerID)
		if response.Code != http.StatusOK {
			t.Fatalf("cancel intent: %d %s", response.Code, response.Body.String())
		}
		receipt := p3DecodeObject(t, response)
		if receipt["accepted"] != false || receipt["cancelled"] != true || receipt["cancellation_status"] != "cancelled_before_acceptance" || receipt["frame_id"] != nil {
			t.Fatalf("tombstone=%#v", receipt)
		}
	}
	late := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if late.Code != http.StatusConflict || p3DecodeObject(t, late)["code"] != "ASIDE_INTENT_CANCELLED" {
		t.Fatalf("late POST: %d %s", late.Code, late.Body.String())
	}
	read := p3JSONRequest(t, f.server, http.MethodGet, query, nil, f.stream.OwnerID)
	if read.Code != http.StatusOK || p3DecodeObject(t, read)["cancelled"] != true {
		t.Fatalf("lookup: %s", read.Body.String())
	}
	changed := copyMapAny(body)
	changed["request"] = "Different operation"
	conflict := p3JSONRequest(t, f.server, http.MethodDelete, query, changed, f.stream.OwnerID)
	if conflict.Code != http.StatusConflict || p3DecodeObject(t, conflict)["code"] != "ASIDE_INTENT_CONFLICT" {
		t.Fatalf("payload conflict: %s", conflict.Body.String())
	}
	foreign := p3JSONRequest(t, f.server, http.MethodDelete, query, body, "other-owner")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign cancel=%d", foreign.Code)
	}
	if _, err := f.store.CreateFrame(workspace.CreateFrameInput{ID: "another-parent", ProjectID: f.stream.ProjectID, AgentName: "OPERON", Status: "completed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	wrongParent := p3JSONRequest(t, f.server, http.MethodDelete, "/api/frames/another-parent/aside?intent_id="+intent, body, f.stream.OwnerID)
	if wrongParent.Code != http.StatusConflict {
		t.Fatalf("wrong-parent intent cancellation=%d", wrongParent.Code)
	}
	var frames, queuedEvents int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frames`).Scan(&frames); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='queued_message'`).Scan(&queuedEvents); err != nil {
		t.Fatal(err)
	}
	if frames != 2 || queuedEvents != 0 {
		t.Fatalf("cancel manufactured work: frames=%d queued=%d", frames, queuedEvents)
	}
	reopened, err := workspace.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, found, err := reopened.GetCompatibilityAsideAcceptance(context.Background(), f.stream.OwnerID, f.stream.FrameID, intent)
	if err != nil || !found || recovered.Cancellation == nil {
		t.Fatalf("restart tombstone=%#v %t %v", recovered, found, err)
	}
}

func TestAsideCancelIntentAfterAcceptanceUsesExactChildInput(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	const intent = "9647fe6a-04cb-4c04-b6eb-3351ba621187"
	path := "/api/frames/" + f.stream.FrameID + "/aside"
	body := map[string]any{"request": "Accepted child", "intent_id": intent}
	created := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if created.Code != http.StatusOK {
		t.Fatalf("create: %s", created.Body.String())
	}
	child := webString(p3DecodeObject(t, created)["frame_id"])
	for repeat := 0; repeat < 2; repeat++ {
		response := p3JSONRequest(t, f.server, http.MethodDelete, path+"?intent_id="+intent, body, f.stream.OwnerID)
		receipt := p3DecodeObject(t, response)
		if response.Code != http.StatusOK || receipt["accepted"] != true || receipt["cancelled"] != true || receipt["cancelled_input_message_id"] != intent || receipt["frame_id"] != child {
			t.Fatalf("child cancel: %d %#v", response.Code, receipt)
		}
	}
	read := p3JSONRequest(t, f.server, http.MethodGet, path+"?intent_id="+intent, nil, f.stream.OwnerID)
	if read.Code != http.StatusOK || p3DecodeObject(t, read)["cancelled_input_message_id"] != intent {
		t.Fatalf("lost cancel receipt lookup: %s", read.Body.String())
	}
	stream, found, err := f.repo.GetFrameStreamBySession(context.Background(), f.stream.OwnerID, child)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, _, _, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "new-owned-followup", FrameEventID: "new-owned-followup-event", MessageUUID: "new-owned-followup-message", Text: "New followup",
	}); err != nil {
		t.Fatal(err)
	}
	stale := p3JSONRequest(t, f.server, http.MethodDelete, path+"?intent_id="+intent, body, f.stream.OwnerID)
	if stale.Code != http.StatusConflict || p3DecodeObject(t, stale)["code"] != "CONVERSATION_CANCEL_INPUT_STALE" {
		t.Fatalf("late Close: %s", stale.Body.String())
	}
	current := copyMapAny(body)
	current["expected_input_message_id"] = "new-owned-followup"
	response := p3JSONRequest(t, f.server, http.MethodDelete, path+"?intent_id="+intent, current, f.stream.OwnerID)
	if response.Code != http.StatusOK || p3DecodeObject(t, response)["cancelled_input_message_id"] != "new-owned-followup" {
		t.Fatalf("owned followup cancel: %s", response.Body.String())
	}
}

func TestAsideCancelIntentRacesCreationAtSameAuthority(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	const intent = "3035c7ad-1ee5-439f-9098-0e1cc9d8d875"
	path := "/api/frames/" + f.stream.FrameID + "/aside"
	body := map[string]any{"request": "Racing original request", "intent_id": intent}
	responses := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		go func(method string) {
			<-start
			target := path
			if method == http.MethodDelete {
				target += "?intent_id=" + intent
			}
			responses <- p3JSONRequest(t, f.server, method, target, body, f.stream.OwnerID)
		}(method)
	}
	close(start)
	for range 2 {
		response := <-responses
		if response.Code != http.StatusOK && response.Code != http.StatusConflict {
			t.Fatalf("race: %d %s", response.Code, response.Body.String())
		}
	}
	var active, children int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frames WHERE id<>? AND status='processing'`, f.stream.FrameID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frames WHERE id<>?`, f.stream.FrameID).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if active != 0 || children > 1 {
		t.Fatalf("late creation escaped cancellation: active=%d children=%d", active, children)
	}
}

func TestAsideCancelIntentDoesNotCancelQueuedFollowup(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	const intent = "69f513ac-dbe3-4613-9fed-d2831f810697"
	path := "/api/frames/" + f.stream.FrameID + "/aside"
	body := map[string]any{"request": "Original task", "intent_id": intent}
	created := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if created.Code != http.StatusOK {
		t.Fatalf("create: %s", created.Body.String())
	}
	child := webString(p3DecodeObject(t, created)["frame_id"])
	if _, _, _, err := f.store.QueueCompatibilityMessage(child, "later-queued", map[string]any{"request": "Queued followup"}, map[string]any{"text": "Queued followup"}); err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.server, http.MethodDelete, path+"?intent_id="+intent, body, f.stream.OwnerID)
	if response.Code != http.StatusConflict || p3DecodeObject(t, response)["code"] != "CONVERSATION_CANCEL_INPUT_STALE" {
		t.Fatalf("queued followup cancelled: %d %s", response.Code, response.Body.String())
	}
	frame, found, err := f.store.GetFrame(child)
	if err != nil || !found || frame.Status != "processing" {
		t.Fatalf("child changed: %#v %v", frame, err)
	}
	queued, found, err := f.store.GetCompatibilityMessageIntent("later-queued")
	if err != nil || !found || queued.State != "queued" {
		t.Fatalf("followup changed: %#v %v", queued, err)
	}
}
