package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func presentationTestFrame(t *testing.T, store *workspace.Store, id string) workspace.Frame {
	t.Helper()
	frame, err := store.CreateFrame(workspace.CreateFrameInput{ID: id, ProjectID: "presentation-project", AgentName: "OPERON", Status: "completed", ConversationType: "agent", Name: id})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{UID: "frame:" + id, OwnerID: "local", ExternalID: id, SessionID: id, Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: id, FrameID: id, Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	frame, _, err = store.GetFrame(id)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestConversationPresentationExtraOnlyHTTPRoundTripAndCAS(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	frame := presentationTestFrame(t, store, "presentation-root")
	body := map[string]any{"extra": map[string]any{"pinned": true}, "merge_extra": true, "expected_incarnation_id": frame.IncarnationID, "expected_presentation_revision": 0}
	path := "/api/conversations/" + frame.ID
	patch := p3JSONRequest(t, app, http.MethodPatch, path, body, "")
	if patch.Code != http.StatusOK {
		t.Fatalf("extra only: %d %s", patch.Code, patch.Body.String())
	}
	for _, path := range []string{path, "/api/conversations?section=pinned"} {
		response := p3JSONRequest(t, app, http.MethodGet, path, nil, "")
		if response.Code != http.StatusOK {
			t.Fatalf("read: %d %s", response.Code, response.Body.String())
		}
		row := p3DecodeObject(t, response)
		if items, ok := row["items"].([]any); ok {
			if len(items) != 1 {
				t.Fatalf("items=%#v", items)
			}
			row = items[0].(map[string]any)
		}
		extra := row["extra"].(map[string]any)
		if extra["pinned"] != true || extra["presentation_revision"] != float64(1) || extra["frame_incarnation_id"] != frame.IncarnationID {
			t.Fatalf("readback=%#v", row)
		}
		if row["modified_at"] != float64(frame.UpdatedAt.UnixMilli()) {
			t.Fatalf("pin changed activity: %#v", row)
		}
	}
	if again := p3JSONRequest(t, app, http.MethodPatch, path, body, ""); again.Code != http.StatusOK {
		t.Fatalf("lost receipt retry: %s", again.Body.String())
	}
	body["extra"] = map[string]any{"pinned": false}
	if stale := p3JSONRequest(t, app, http.MethodPatch, path, body, ""); stale.Code != http.StatusConflict {
		t.Fatalf("stale overwrite: %d %s", stale.Code, stale.Body.String())
	}
	for _, extra := range []map[string]any{{"backend": "other"}, {"workspace": "/private"}, {"pinned_at": 1}, {"pinned": "yes"}} {
		bad := p3JSONRequest(t, app, http.MethodPatch, path, map[string]any{"extra": extra}, "")
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("invalid extra: %d %s", bad.Code, bad.Body.String())
		}
	}
	if other := p3JSONRequest(t, app, http.MethodPatch, path, body, "other-owner"); other.Code != http.StatusNotFound {
		t.Fatalf("cross owner: %d %s", other.Code, other.Body.String())
	}
}

func TestConversationPresentationPinnedPagesAreIndependentAndCursorBound(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	var oldest workspace.Frame
	for i := 0; i < 103; i++ {
		frame := presentationTestFrame(t, store, fmt.Sprintf("pin-page-%03d", i))
		if i == 0 {
			oldest = frame
		}
	}
	zero := int64(0)
	if _, err := store.UpdateConversationPresentation(context.Background(), workspace.ConversationPresentationUpdate{OwnerID: "local", FrameID: oldest.ID, IncarnationID: oldest.IncarnationID, ExpectedRevision: &zero, Extra: map[string]any{"pinned": true}}); err != nil {
		t.Fatal(err)
	}
	pinned := p3JSONRequest(t, app, http.MethodGet, "/api/conversations?section=pinned&limit=1", nil, "")
	if pinned.Code != http.StatusOK {
		t.Fatalf("pin page: %s", pinned.Body.String())
	}
	items := p3DecodeObject(t, pinned)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != oldest.ID {
		t.Fatalf("old pin missing: %#v", items)
	}
	recent := p3JSONRequest(t, app, http.MethodGet, "/api/conversations?section=recent&limit=1", nil, "")
	page := p3DecodeObject(t, recent)
	cursor := webString(page["next_cursor"])
	if cursor == "" {
		t.Fatal("missing recent cursor")
	}
	for _, path := range []string{"/api/conversations?section=pinned&cursor=", "/api/conversations?cursor="} {
		bad := p3JSONRequest(t, app, http.MethodGet, path+url.QueryEscape(cursor), nil, "")
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("cross section cursor: %d %s", bad.Code, bad.Body.String())
		}
	}
	badOwner := p3JSONRequest(t, app, http.MethodGet, "/api/conversations?section=recent&cursor="+url.QueryEscape(cursor), nil, "other-owner")
	if badOwner.Code != http.StatusBadRequest {
		t.Fatalf("cross owner cursor: %d %s", badOwner.Code, badOwner.Body.String())
	}
	legacy := p3JSONRequest(t, app, http.MethodGet, "/api/conversations?limit=1", nil, "")
	legacyCursor := webString(p3DecodeObject(t, legacy)["next_cursor"])
	if next := p3JSONRequest(t, app, http.MethodGet, "/api/conversations?cursor="+url.QueryEscape(legacyCursor), nil, ""); next.Code != http.StatusOK {
		t.Fatalf("legacy cursor: %d %s", next.Code, next.Body.String())
	}
	for i := 1; i < 103; i++ {
		frame, _, err := store.GetFrame(fmt.Sprintf("pin-page-%03d", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateConversationPresentation(context.Background(), workspace.ConversationPresentationUpdate{OwnerID: "local", FrameID: frame.ID, IncarnationID: frame.IncarnationID, ExpectedRevision: &zero, Extra: map[string]any{"pinned": true}}); err != nil {
			t.Fatal(err)
		}
	}
	firstPins := p3DecodeObject(t, p3JSONRequest(t, app, http.MethodGet, "/api/conversations?section=pinned&limit=100", nil, ""))
	if len(firstPins["items"].([]any)) != 100 || firstPins["has_more"] != true {
		t.Fatalf("first pin page: %#v", firstPins)
	}
	lastPins := p3DecodeObject(t, p3JSONRequest(t, app, http.MethodGet, "/api/conversations?section=pinned&limit=100&cursor="+url.QueryEscape(webString(firstPins["next_cursor"])), nil, ""))
	if len(lastPins["items"].([]any)) != 3 || lastPins["has_more"] != false {
		t.Fatalf("remaining pins: %#v", lastPins)
	}
}
