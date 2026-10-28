package server

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"testing"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureCandidateHTTPRestoreUsesOwnedOriginalOperation(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	stage := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/candidates", map[string]any{
		"operation_id": uuid.NewString(), "provider": "semantic-scholar", "external_id": "restore-fixture", "title": "Restore fixture", "metadata": map[string]any{"precise": json.Number("9007199254740993")},
	}, owner)
	var staged workspace.LiteratureCandidateReceipt
	if stage.Code != http.StatusOK || json.Unmarshal(stage.Body.Bytes(), &staged) != nil {
		t.Fatalf("stage HTTP%d %s", stage.Code, stage.Body.String())
	}
	base := "/api/literature/candidates/" + staged.Candidate.ID
	dismiss := p3JSONRequest(t, f.f.server, http.MethodPost, base+"/dismiss", map[string]any{"operation_id": uuid.NewString()}, owner)
	if dismiss.Code != http.StatusOK {
		t.Fatalf("dismiss HTTP%d %s", dismiss.Code, dismiss.Body.String())
	}
	operation := map[string]any{"operation_id": uuid.NewString()}
	first := p3JSONRequest(t, f.f.server, http.MethodPost, base+"/restore", operation, owner)
	var receipt workspace.LiteratureCandidateReceipt
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &receipt) != nil || receipt.Action != "restore" || receipt.Candidate.State != "pending" || receipt.Candidate.ItemID != "" || !bytes.Contains(first.Body.Bytes(), []byte("9007199254740993")) {
		t.Fatalf("restore HTTP%d %s", first.Code, first.Body.String())
	}
	page := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/candidates?state=pending", nil, owner)
	var pending workspace.LiteratureCandidatePage
	if page.Code != http.StatusOK || json.Unmarshal(page.Body.Bytes(), &pending) != nil || len(pending.Candidates) != 1 || pending.Candidates[0].ID != staged.Candidate.ID {
		t.Fatalf("pending readback HTTP%d %s", page.Code, page.Body.String())
	}
	foreign := p3JSONRequest(t, f.f.server, http.MethodPost, base+"/restore", operation, uuid.NewString())
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign restore HTTP%d %s", foreign.Code, foreign.Body.String())
	}
	accepted := p3JSONRequest(t, f.f.server, http.MethodPost, base+"/accept", map[string]any{"operation_id": uuid.NewString()}, owner)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept HTTP%d %s", accepted.Code, accepted.Body.String())
	}
	replay := p3JSONRequest(t, f.f.server, http.MethodPost, base+"/restore", operation, owner)
	if replay.Code != http.StatusOK || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) {
		t.Fatalf("frozen restore replay HTTP%d %s", replay.Code, replay.Body.String())
	}
	conflict := p3JSONRequest(t, f.f.server, http.MethodPost, base+"/restore", map[string]any{"operation_id": uuid.NewString()}, owner)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("new restore of accepted reference HTTP%d", conflict.Code)
	}
}

func TestLiteratureCandidateHTTPRoutesOwnerLedgerAndReadback(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	op := uuid.NewString()
	body := map[string]any{"operation_id": op, "provider": "semantic-scholar", "external_id": "synthetic", "title": "Candidate", "metadata": map[string]any{"pmid": "12345", "precise": json.Number("9007199254740993")}, "source": map[string]any{"item_id": f.itemID, "metadata_revision": 1, "direction": "references"}}
	first := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/candidates", body, owner)
	var stage workspace.LiteratureCandidateReceipt
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &stage) != nil || stage.Candidate.State != "pending" || stage.OperationID != op || !bytes.Contains(first.Body.Bytes(), []byte("9007199254740993")) {
		t.Fatalf("stage HTTP%d %s", first.Code, first.Body.String())
	}
	updated := p3JSONRequest(t, f.f.server, http.MethodPatch, "/api/literature/items/"+f.itemID, map[string]any{"operation_id": uuid.NewString(), "expected_revision": 1, "title": "Updated source"}, owner)
	if updated.Code != http.StatusOK {
		t.Fatalf("update source HTTP%d %s", updated.Code, updated.Body.String())
	}
	replay := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/candidates", body, owner)
	if replay.Code != http.StatusOK || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) {
		t.Fatalf("replay HTTP%d", replay.Code)
	}
	stale := map[string]any{"operation_id": uuid.NewString(), "provider": "semantic-scholar", "external_id": "other", "title": "Other", "metadata": map[string]any{}, "source": map[string]any{"item_id": f.itemID, "metadata_revision": 1, "direction": "references"}}
	if response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/candidates", stale, owner); response.Code != http.StatusConflict {
		t.Fatalf("stale seed HTTP%d", response.Code)
	}
	page := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/candidates?state=pending", nil, owner)
	var got workspace.LiteratureCandidatePage
	if page.Code != http.StatusOK || json.Unmarshal(page.Body.Bytes(), &got) != nil || len(got.Candidates) != 1 {
		t.Fatalf("read HTTP%d %s", page.Code, page.Body.String())
	}
	foreign := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/candidates/"+stage.Candidate.ID+"/accept", map[string]any{"operation_id": uuid.NewString()}, uuid.NewString())
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign HTTP%d", foreign.Code)
	}
	accept := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/candidates/"+stage.Candidate.ID+"/accept", map[string]any{"operation_id": uuid.NewString()}, owner)
	var receipt workspace.LiteratureCandidateReceipt
	if accept.Code != http.StatusOK || json.Unmarshal(accept.Body.Bytes(), &receipt) != nil || receipt.Candidate.ItemID == "" {
		t.Fatalf("accept HTTP%d %s", accept.Code, accept.Body.String())
	}
	item := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items/"+receipt.Candidate.ItemID, nil, owner)
	if item.Code != http.StatusOK {
		t.Fatalf("accepted item HTTP%d %s", item.Code, item.Body.String())
	}
}
