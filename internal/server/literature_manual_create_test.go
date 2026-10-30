package server

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"testing"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualLiteratureHTTPOriginalOwnerReceiptAndReadback(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	body := map[string]any{"operation_id": uuid.NewString(), "title": "Manual HTTP reference", "metadata": map[string]any{"doi": "10.1234/manual-http", "precise": json.Number("9007199254740993")}}
	first := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/items", body, owner)
	var receipt workspace.LiteratureReceipt
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &receipt) != nil || receipt.Item.ID == "" || len(receipt.Item.Attachments) != 0 || !bytes.Contains(first.Body.Bytes(), []byte("9007199254740993")) {
		t.Fatalf("manual HTTP%d %s", first.Code, first.Body.String())
	}
	replay := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/items", body, owner)
	if replay.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("frozen receipt HTTP%d %s", replay.Code, replay.Body.String())
	}
	detail := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items/"+receipt.Item.ID, nil, owner)
	if detail.Code != http.StatusOK || !bytes.Contains(detail.Body.Bytes(), []byte("Manual HTTP reference")) {
		t.Fatalf("canonical detail HTTP%d %s", detail.Code, detail.Body.String())
	}
	foreign := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items/"+receipt.Item.ID, nil, uuid.NewString())
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign read HTTP%d", foreign.Code)
	}
	body["provider"] = "fake-source"
	invalid := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/items", body, owner)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unknown source payload HTTP%d", invalid.Code)
	}
	yes := true
	if _, err := f.f.store.PatchLiteratureItem(t.Context(), owner, receipt.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &yes}); err != nil {
		t.Fatal(err)
	}
	delete(body, "provider")
	body["operation_id"] = uuid.NewString()
	conflict := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/items", body, owner)
	if conflict.Code != http.StatusConflict || !bytes.Contains(conflict.Body.Bytes(), []byte("LITERATURE_TRASH_CONFLICT")) {
		t.Fatalf("Trash conflict HTTP%d %s", conflict.Code, conflict.Body.String())
	}
}
