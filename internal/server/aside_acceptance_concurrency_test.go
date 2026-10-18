package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAsideAcceptanceConcurrentSameIntentCreatesOneChild(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	const intent = "221378ec-99b5-4c6b-8af0-98c1c0d6ce89"
	request := map[string]any{"request": "One independent task", "intent_id": intent, "as_session": false}
	responses := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for index := 0; index < 2; index++ {
		go func() {
			<-start
			responses <- p3JSONRequest(t, f.server, http.MethodPost, "/api/frames/"+f.stream.FrameID+"/aside", request, f.stream.OwnerID)
		}()
	}
	close(start)
	child := ""
	for index := 0; index < 2; index++ {
		response := <-responses
		if response.Code != http.StatusOK {
			t.Fatalf("accept: %d %s", response.Code, response.Body.String())
		}
		id := webString(p3DecodeObject(t, response)["frame_id"])
		if id == "" || child != "" && id != child {
			t.Fatalf("two children: %q %q", child, id)
		}
		child = id
	}
	var frames, inputs int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frames WHERE id<>?`, f.stream.FrameID).Scan(&frames); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='user_message'`, "frame:"+child).Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	if frames != 1 || inputs != 1 {
		t.Fatalf("duplicate acceptance: frames=%d inputs=%d", frames, inputs)
	}
}
