package server

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func assertUnknownArchivePresentation(t *testing.T, conversation map[string]any, id string) {
	t.Helper()
	extra, valid := conversation["extra"].(map[string]any)
	if conversation["id"] != id || !valid || extra["presentation_unavailable"] != true {
		t.Fatalf("unknown presentation was hidden or treated as active: %#v", conversation)
	}
	for _, key := range []string{"presentation_revision", "pinned", "pinned_at", "sortOrder", "archived_at"} {
		if value, present := extra[key]; present {
			t.Errorf("unknown presentation supplied usable %q authority=%#v", key, value)
		}
	}
}

func TestConversationArchiveUnknownPresentationDoesNotBreakReadOnlyListOrDetail(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	bad := presentationTestFrame(t, store, "archive-opaque-readable")
	good := presentationTestFrame(t, store, "archive-valid-readable")
	if _, err := store.SetFrameRuntimeMetadata(bad.ID, workspace.FrameRuntimeMetadata{
		FrameID: bad.ID, ContextData: map[string]any{"web_extra": "opaque-legacy-presentation", "runtime_owned": "retain"},
	}); err != nil {
		t.Fatal(err)
	}
	beforeFrame, beforeMetadata := archiveHTTPState(t, store, bad.ID)
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beforeStream, err := repo.GetStream(context.Background(), "frame:"+bad.ID, "local")
	if err != nil {
		t.Fatal(err)
	}
	listResponse := p3JSONRequest(t, app, http.MethodGet, "/api/conversations?limit=10", nil, "")
	if listResponse.Code != http.StatusOK {
		t.Fatalf("one unknown presentation broke the entire list: status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	list := p3DecodeObject(t, listResponse)
	rows := list["items"].([]any)
	if len(rows) != 2 {
		t.Fatalf("unknown conversation was silently dropped: %#v", list)
	}
	found := map[string]map[string]any{}
	for _, row := range rows {
		conversation := row.(map[string]any)
		found[conversation["id"].(string)] = conversation
	}
	assertUnknownArchivePresentation(t, found[bad.ID], bad.ID)
	goodExtra := found[good.ID]["extra"].(map[string]any)
	if goodExtra["presentation_unavailable"] == true || goodExtra["presentation_revision"] != float64(0) {
		t.Fatalf("bad row poisoned valid presentation: %#v", found[good.ID])
	}
	detailResponse := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+bad.ID, nil, "")
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("unknown presentation blocked read-only detail: status=%d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	assertUnknownArchivePresentation(t, p3DecodeObject(t, detailResponse), bad.ID)
	goodResponse := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+good.ID, nil, "")
	if goodResponse.Code != http.StatusOK || p3DecodeObject(t, goodResponse)["extra"].(map[string]any)["presentation_revision"] != float64(0) {
		t.Fatalf("valid detail changed: status=%d body=%s", goodResponse.Code, goodResponse.Body.String())
	}
	afterFrame, afterMetadata := archiveHTTPState(t, store, bad.ID)
	afterStream, err := repo.GetStream(context.Background(), beforeStream.UID, beforeStream.OwnerID)
	if err != nil || !reflect.DeepEqual(beforeFrame, afterFrame) || !reflect.DeepEqual(beforeMetadata, afterMetadata) || !reflect.DeepEqual(beforeStream, afterStream) {
		t.Fatalf("read-only unknown display changed canonical data: frame=%#v metadata=%#v stream=%#v err=%v", afterFrame, afterMetadata, afterStream, err)
	}
}

func TestConversationArchiveReadOnlyFlagCannotOverrideUnknownAuthority(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	frame := presentationTestFrame(t, store, "archive-unknown-flag")
	if _, err := store.SetFrameRuntimeMetadata(frame.ID, workspace.FrameRuntimeMetadata{
		FrameID: frame.ID, ContextData: map[string]any{"web_extra": map[string]any{
			"archived_at": "unknown", "presentation_unavailable": false,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + frame.ID
	response := p3JSONRequest(t, app, http.MethodGet, path, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("unknown detail status=%d body=%s", response.Code, response.Body.String())
	}
	assertUnknownArchivePresentation(t, p3DecodeObject(t, response), frame.ID)
	response = p3JSONRequest(t, app, http.MethodPatch, path, map[string]any{
		"extra": map[string]any{"pinned": true}, "merge_extra": true,
		"expected_incarnation_id": frame.IncarnationID, "expected_presentation_revision": 0,
	}, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown Pin authority was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	response = p3JSONRequest(t, app, http.MethodPost, path+"/archive", archiveHTTPRequestBody(frame, true, 0), "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown archive authority was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	// Generic runtime updates deliberately preserve an existing web_extra
	// object. Seed this independent corruption on a new identity instead of
	// mistakenly reusing the unknown archived_at object's protected envelope.
	invalidRevision := presentationTestFrame(t, store, "archive-invalid-revision")
	if _, err := store.SetFrameRuntimeMetadata(invalidRevision.ID, workspace.FrameRuntimeMetadata{
		FrameID: invalidRevision.ID, ContextData: map[string]any{"web_extra": map[string]any{"_presentation_revision": "invalid"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, persisted := archiveHTTPState(t, store, invalidRevision.ID)
	extra := persisted.ContextData["web_extra"].(map[string]any)
	if extra["_presentation_revision"] != "invalid" {
		t.Fatalf("independent invalid-revision fixture was not persisted: %#v", persisted)
	}
	if _, present := extra["archived_at"]; present {
		t.Fatalf("invalid revision fixture inherited unknown archive authority: %#v", persisted)
	}
	if response := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+invalidRevision.ID, nil, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("display adapter swallowed a different presentation error: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestConversationArchiveReadOnlyFlagIsNotPersistedOnCreate(t *testing.T) {
	for _, supplied := range []bool{true, false} {
		t.Run(map[bool]string{true: "true", false: "false"}[supplied], func(t *testing.T) {
			app, store := newP3WebConversationServer(t)
			createP3Project(t, store, "presentation-project", "local")
			response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations", map[string]any{
				"name": "New conversation", "extra": map[string]any{"project_id": "presentation-project", "presentation_unavailable": supplied},
			}, "")
			if response.Code != http.StatusCreated {
				t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
			}
			id := p3DecodeObject(t, response)["id"].(string)
			_, metadata := archiveHTTPState(t, store, id)
			if value, present := metadata.ContextData["web_extra"].(map[string]any)["presentation_unavailable"]; present {
				t.Fatalf("create persisted read-only projection=%#v", value)
			}
			response = p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+id, nil, "")
			if response.Code != http.StatusOK {
				t.Fatalf("created detail status=%d body=%s", response.Code, response.Body.String())
			}
			extra := p3DecodeObject(t, response)["extra"].(map[string]any)
			if _, present := extra["presentation_unavailable"]; present || extra["presentation_revision"] != float64(0) {
				t.Fatalf("client flag became presentation authority: %#v", extra)
			}
		})
	}
}
