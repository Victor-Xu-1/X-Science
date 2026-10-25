package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureItemDetailReadsExistingOwnerRecord(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	seeded := seedLiteratureMetadataPatch(t, f)
	path := "/api/literature/items/" + f.itemID
	read := func(owner string) *http.Response {
		t.Helper()
		return p3JSONRequest(t, f.f.server, http.MethodGet, path, nil, owner).Result()
	}
	var ledgerBefore int
	if err := f.f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM mutation_result_ledger").Scan(&ledgerBefore); err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.f.server, http.MethodGet, path, nil, f.f.stream.OwnerID)
	var item workspace.LiteratureItem
	decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
	decoder.UseNumber()
	if response.Code != http.StatusOK || decoder.Decode(&item) != nil ||
		item.ID != seeded.Item.ID || item.Title != seeded.Item.Title ||
		!bytes.Equal(literatureHTTPNumberJSON(t, item.Metadata), literatureHTTPNumberJSON(t, seeded.Item.Metadata)) {
		t.Fatalf("detail did not read the original owner-authorized catalogue object: HTTP%d", response.Code)
	}
	if strings.Contains(response.Body.String(), "storage_path") || strings.Contains(response.Body.String(), "StoragePath") {
		t.Fatal("detail exposed private storage paths")
	}
	foreign := read("another-owner")
	defer foreign.Body.Close()
	if foreign.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign owner received detail: HTTP%d", foreign.StatusCode)
	}
	badQuery := p3JSONRequest(t, f.f.server, http.MethodGet, path+"?unexpected=1", nil, f.f.stream.OwnerID)
	if badQuery.Code != http.StatusBadRequest {
		t.Fatalf("unbound detail query was accepted: HTTP%d", badQuery.Code)
	}
	var ledgerAfter int
	if err := f.f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM mutation_result_ledger").Scan(&ledgerAfter); err != nil || ledgerAfter != ledgerBefore {
		t.Fatalf("read changed mutation receipts: before=%d after=%d err=%v", ledgerBefore, ledgerAfter, err)
	}
	title := "Updated catalogue title"
	updated, err := f.f.store.PatchLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID, workspace.LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: seeded.Item.MetadataRevision, Title: &title,
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := p3JSONRequest(t, f.f.server, http.MethodGet, path, nil, f.f.stream.OwnerID)
	if fresh.Code != http.StatusOK || !strings.Contains(fresh.Body.String(), title) {
		t.Fatal("detail did not return the current persisted metadata")
	}
	deleted := true
	if _, err := f.f.store.PatchLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID, workspace.LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: updated.Item.MetadataRevision, Deleted: &deleted,
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(f.f.stream.OwnerID); got.StatusCode != http.StatusNotFound {
		got.Body.Close()
		t.Fatalf("deleted item remained in the active workbench: HTTP%d", got.StatusCode)
	} else {
		got.Body.Close()
	}
}
