package server

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"testing"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureCandidateHTTPRoutesOwnerLedgerAndReadback(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	op := uuid.NewString()
	body := map[string]any{"operation_id": op, "provider": "semantic-scholar", "external_id": "synthetic", "title": "Candidate", "metadata": map[string]any{"pmid": "12345", "precise": json.Number("9007199254740993")}}
	first := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/candidates", body, owner)
	var stage workspace.LiteratureCandidateReceipt
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &stage) != nil || stage.Candidate.State != "pending" || stage.OperationID != op || !bytes.Contains(first.Body.Bytes(), []byte("9007199254740993")) {
		t.Fatalf("stage HTTP%d %s", first.Code, first.Body.String())
	}
	replay := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/candidates", body, owner)
	if replay.Code != http.StatusOK || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) {
		t.Fatalf("replay HTTP%d", replay.Code)
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
