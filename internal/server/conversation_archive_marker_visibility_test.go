package server

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func archiveMarkerVisibilityPages(t *testing.T, app *Server, section string) map[string]map[string]any {
	t.Helper()
	rows := map[string]map[string]any{}
	cursors := map[string]bool{}
	cursor := ""
	for page := 0; page < 8; page++ {
		path := "/api/conversations?limit=1"
		if section != "" {
			path += "&section=" + url.QueryEscape(section)
		}
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		response := p3JSONRequest(t, app, http.MethodGet, path, nil, "")
		if response.Code != http.StatusOK {
			t.Fatalf("section=%q page=%d status=%d body=%s", section, page, response.Code, response.Body.String())
		}
		body := p3DecodeObject(t, response)
		items := body["items"].([]any)
		if len(items) > 1 {
			t.Fatalf("section=%q exceeded its real SQL page limit: %#v", section, body)
		}
		for _, item := range items {
			row := item.(map[string]any)
			id := row["id"].(string)
			if _, duplicate := rows[id]; duplicate {
				t.Fatalf("section=%q repeated a conversation across pages: %s", section, id)
			}
			rows[id] = row
		}
		if body["has_more"] != true {
			return rows
		}
		cursor, _ = body["next_cursor"].(string)
		if cursor == "" || cursors[cursor] {
			t.Fatalf("section=%q supplied a missing/repeated continuation: %#v", section, body)
		}
		cursors[cursor] = true
	}
	t.Fatalf("section=%q failed to terminate five-row fixture pagination", section)
	return nil
}

func TestConversationArchiveInvalidMarkersRemainVisibleBeforePagination(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	badIDs := []string{}
	for _, marker := range []struct {
		id    string
		value any
	}{
		{"archive-marker-string", "unknown"},
		{"archive-marker-negative", int64(-1)},
		{"archive-marker-fractional", 0.5},
	} {
		frame := presentationTestFrame(t, store, marker.id)
		if _, err := store.SetFrameRuntimeMetadata(frame.ID, workspace.FrameRuntimeMetadata{
			FrameID: frame.ID, ContextData: map[string]any{"web_extra": map[string]any{"archived_at": marker.value}},
		}); err != nil {
			t.Fatal(err)
		}
		_, persisted := archiveHTTPState(t, store, frame.ID)
		if _, err := workspace.ConversationPresentation(persisted.ContextData); !errors.Is(err, transcriptstore.ErrConversationArchiveInvalid) {
			t.Fatalf("invalid-marker fixture is not durably unknown: id=%s metadata=%#v err=%v", frame.ID, persisted, err)
		}
		badIDs = append(badIDs, frame.ID)
	}
	good := presentationTestFrame(t, store, "archive-marker-good")
	archived := presentationTestFrame(t, store, "archive-marker-valid-archived")
	response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+archived.ID+"/archive", archiveHTTPRequestBody(archived, true, 0), "")
	if response.Code != http.StatusOK {
		t.Fatalf("canonical legal archive status=%d body=%s", response.Code, response.Body.String())
	}
	t.Run("default-pagination-retains-unknown", func(t *testing.T) {
		rows := archiveMarkerVisibilityPages(t, app, "")
		if len(rows) != 4 {
			t.Errorf("SQL classified unknown archive authority as hidden: got=%d rows=%#v", len(rows), rows)
		}
		for _, id := range badIDs {
			assertUnknownArchivePresentation(t, rows[id], id)
		}
		goodRow, present := rows[good.ID]
		if !present || goodRow["extra"].(map[string]any)["presentation_revision"] != float64(0) {
			t.Fatalf("valid visible row was lost or changed: %#v", rows)
		}
		if _, leaked := rows[archived.ID]; leaked {
			t.Fatalf("real legal archive leaked into default pagination: %#v", rows[archived.ID])
		}
	})
	t.Run("archived-pagination-requires-valid-authority", func(t *testing.T) {
		rows := archiveMarkerVisibilityPages(t, app, "archived")
		if len(rows) != 1 {
			t.Errorf("SQL minted archive classification from an invalid marker: %#v", rows)
		}
		for _, id := range badIDs {
			if row, leaked := rows[id]; leaked {
				t.Errorf("unknown row was classified as archived: id=%s row=%#v", id, row)
			}
		}
		row, present := rows[archived.ID]
		if !present {
			t.Fatalf("canonical archive disappeared: %#v", rows)
		}
		extra := row["extra"].(map[string]any)
		if extra["presentation_revision"] != float64(1) || extra["archived_at"] == nil || extra["presentation_unavailable"] == true {
			t.Fatalf("legal archive projection changed: %#v", row)
		}
	})
}
