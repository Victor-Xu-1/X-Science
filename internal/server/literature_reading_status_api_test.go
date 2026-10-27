package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureReadingStatusHTTPUsesOwnerLedgerAndFilteredRouter(t *testing.T) {
	// The existing loopback fixture exercises the real router and Store owner
	// boundary; it does not represent browser login or cookie/CSRF acceptance.
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	before, err := f.f.store.GetLiteratureItem(t.Context(), owner, f.itemID)
	if err != nil {
		t.Fatal(err)
	}
	const path = "/api/literature/items/reading-status"
	operation := uuid.NewString()
	body := map[string]any{"operation_id": operation, "item_ids": []string{f.itemID}, "reading_status": "read"}
	first := p3JSONRequest(t, f.f.server, http.MethodPost, path, body, owner)
	var receipt workspace.LiteratureReadingStatusReceipt
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &receipt) != nil ||
		receipt.Schema != "x-science.literature-reading-status-receipt.v1" || receipt.OperationID != operation ||
		receipt.ReadingStatus != workspace.LiteratureRead || !reflect.DeepEqual(receipt.ItemIDs, []string{f.itemID}) {
		t.Fatalf("POST was not acknowledged through the router: HTTP%d %s", first.Code, first.Body.String())
	}
	newer := p3JSONRequest(t, f.f.server, http.MethodPost, path,
		map[string]any{"operation_id": uuid.NewString(), "item_ids": []string{f.itemID}, "reading_status": "reading"}, owner)
	if newer.Code != http.StatusOK {
		t.Fatalf("later independent status failed: HTTP%d %s", newer.Code, newer.Body.String())
	}
	replay := p3JSONRequest(t, f.f.server, http.MethodPost, path, body, owner)
	after, err := f.f.store.GetLiteratureItem(t.Context(), owner, f.itemID)
	if replay.Code != http.StatusOK || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) || err != nil ||
		after.ReadingStatus != workspace.LiteratureReading || after.MetadataRevision != before.MetadataRevision || !reflect.DeepEqual(after.Metadata, before.Metadata) {
		t.Fatalf("original ACK replay rewrote current status or metadata: HTTP%d item%+v err%v", replay.Code, after, err)
	}
	for _, tc := range []struct {
		status string
		want   []string
	}{
		{"read", []string{}},
		{"reading", []string{f.itemID}},
	} {
		response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?reading_status="+tc.status, nil, owner)
		var page workspace.LiteraturePage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Schema != "x-science.literature-page.v1" {
			t.Fatalf("GET status filter was not routed: HTTP%d %s", response.Code, response.Body.String())
		}
		ids := make([]string, len(page.Items))
		for index, item := range page.Items {
			ids[index] = item.ID
			if item.ReadingStatus != workspace.LiteratureReadingStatus(tc.status) {
				t.Fatalf("filtered response lost stored status: %+v", item)
			}
		}
		if !reflect.DeepEqual(ids, tc.want) {
			t.Fatalf("GET filter returned wrong references: %v want%v", ids, tc.want)
		}
	}
	for _, tc := range []struct {
		method, target string
		body           any
		owner          string
		status         int
	}{
		{http.MethodPost, path, body, uuid.NewString(), http.StatusNotFound},
		{http.MethodPost, path, map[string]any{"operation_id": uuid.NewString(), "item_ids": []string{f.itemID}, "reading_status": "unread", "metadata": map[string]any{}}, owner, http.StatusBadRequest},
		{http.MethodPost, path, map[string]any{"operation_id": operation, "item_ids": []string{f.itemID}, "reading_status": "unread"}, owner, http.StatusConflict},
		{http.MethodGet, path, nil, owner, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/literature/items?reading_status=archived", nil, owner, http.StatusBadRequest},
	} {
		response := p3JSONRequest(t, f.f.server, tc.method, tc.target, tc.body, tc.owner)
		if response.Code != tc.status || tc.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != "POST" {
			t.Fatalf("route guard %s %s: HTTP%d want%d %s", tc.method, tc.target, response.Code, tc.status, response.Body.String())
		}
	}
	unchanged, err := f.f.store.GetLiteratureItem(t.Context(), owner, f.itemID)
	if err != nil || unchanged.ReadingStatus != workspace.LiteratureReading || unchanged.MetadataRevision != before.MetadataRevision {
		t.Fatalf("rejected request changed the current reference: %+v %v", unchanged, err)
	}
}
