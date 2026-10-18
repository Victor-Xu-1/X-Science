package server

import (
	"net/http"
	"net/url"
	"testing"
)

func TestConversationArchiveHTTPReturnsExactPresentationAndChanged(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	frame := presentationTestFrame(t, store, "archive-command-receipt")
	path := "/api/conversations/" + frame.ID + "/archive"
	response := p3JSONRequest(t, app, http.MethodPost, path, archiveHTTPRequestBody(frame, true, 0), "")
	if response.Code != http.StatusOK {
		t.Fatalf("archive status=%d body=%s", response.Code, response.Body.String())
	}
	receipt := p3DecodeObject(t, response)
	conversation, valid := receipt["conversation"].(map[string]any)
	if !valid || receipt["changed"] != true || conversation["id"] != frame.ID {
		t.Fatalf("typed command receipt=%#v", receipt)
	}
	extra := conversation["extra"].(map[string]any)
	if extra["frame_incarnation_id"] != frame.IncarnationID || extra["presentation_revision"] != float64(1) || extra["archived_at"] == nil {
		t.Fatalf("Undo authority did not come from the archive transaction: %#v", extra)
	}
	response = p3JSONRequest(t, app, http.MethodPost, path, archiveHTTPRequestBody(frame, true, 1), "")
	if response.Code != http.StatusOK || p3DecodeObject(t, response)["changed"] != false {
		t.Fatalf("same-version noop status=%d body=%s", response.Code, response.Body.String())
	}
	response = p3JSONRequest(t, app, http.MethodPatch, "/api/conversations/"+frame.ID, map[string]any{
		"extra": map[string]any{"pinned": true}, "merge_extra": true,
		"expected_incarnation_id": frame.IncarnationID, "expected_presentation_revision": 1,
	}, "")
	if response.Code != http.StatusGone || p3DecodeObject(t, response)["code"] != "CONVERSATION_ARCHIVED" {
		t.Fatalf("archived action did not return its typed availability boundary: status=%d body=%s", response.Code, response.Body.String())
	}
	response = p3JSONRequest(t, app, http.MethodPost, path, archiveHTTPRequestBody(frame, false, 1), "")
	if response.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", response.Code, response.Body.String())
	}
	receipt = p3DecodeObject(t, response)
	extra = receipt["conversation"].(map[string]any)["extra"].(map[string]any)
	if receipt["changed"] != true || extra["presentation_revision"] != float64(2) || extra["archived_at"] != nil || extra["frame_incarnation_id"] != frame.IncarnationID {
		t.Fatalf("restore receipt=%#v", receipt)
	}
}

func TestConversationArchiveHTTPPaginationBindsOwnerAndSection(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	for _, id := range []string{"archive-list-a", "archive-list-b"} {
		frame := presentationTestFrame(t, store, id)
		if response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+id+"/archive", archiveHTTPRequestBody(frame, true, 0), ""); response.Code != http.StatusOK {
			t.Fatalf("archive %s status=%d body=%s", id, response.Code, response.Body.String())
		}
	}
	active := presentationTestFrame(t, store, "archive-list-active")
	firstResponse := p3JSONRequest(t, app, http.MethodGet, "/api/conversations?section=archived&limit=1", nil, "")
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("archive list status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	first := p3DecodeObject(t, firstResponse)
	items := first["items"].([]any)
	cursor, valid := first["next_cursor"].(string)
	if len(items) != 1 || first["has_more"] != true || !valid || cursor == "" {
		t.Fatalf("first archive page=%#v", first)
	}
	secondResponse := p3JSONRequest(t, app, http.MethodGet, "/api/conversations?section=archived&limit=1&cursor="+url.QueryEscape(cursor), nil, "")
	if secondResponse.Code != http.StatusOK {
		t.Fatalf("second archive page status=%d body=%s", secondResponse.Code, secondResponse.Body.String())
	}
	second := p3DecodeObject(t, secondResponse)
	secondItems := second["items"].([]any)
	if len(secondItems) != 1 || second["has_more"] != false ||
		items[0].(map[string]any)["id"] == secondItems[0].(map[string]any)["id"] {
		t.Fatalf("archive pagination repeated or lost a row: first=%#v second=%#v", first, second)
	}
	for _, test := range []struct{ section, owner string }{{"recent", ""}, {"pinned", ""}, {"archived", "foreign-owner"}} {
		response := p3JSONRequest(t, app, http.MethodGet, "/api/conversations?section="+test.section+"&cursor="+url.QueryEscape(cursor), nil, test.owner)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("cross-scope cursor section=%s owner=%s status=%d", test.section, test.owner, response.Code)
		}
	}
	visible := p3DecodeObject(t, p3JSONRequest(t, app, http.MethodGet, "/api/conversations?section=recent&limit=1", nil, ""))
	if rows := visible["items"].([]any); len(rows) != 1 || rows[0].(map[string]any)["id"] != active.ID {
		t.Fatalf("active page includes archived rows: %#v", visible)
	}
}
