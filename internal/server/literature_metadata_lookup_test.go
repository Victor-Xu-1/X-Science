package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"x-science/internal/literaturediscovery"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureDOILookupRejectsUnsupportedBeforeMutation(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	owner := f.stream.OwnerID
	before := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/items?limit=20", nil, owner)
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/metadata-lookup", map[string]any{"reference": "arxiv:2401.12345"}, owner)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("DOI-only read contract missing: got HTTP%d", response.Code)
	}
	after := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/items?limit=20", nil, owner)
	if before.Code != http.StatusOK || after.Code != http.StatusOK || before.Body.String() != after.Body.String() {
		t.Fatal("rejected metadata lookup changed the catalogue")
	}
}

func TestLiteratureDOILookupIsReadOnlyUntilOriginalManualCreate(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	owner := f.stream.OwnerID
	calls := 0
	f.server.doiMetadataLookup = literaturediscovery.NewDOIMetadataLookup(func(context.Context, string) (*http.Client, error) {
		return &http.Client{Transport: publicationHTTPTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":"ok","message-type":"work","message":{"DOI":"10.1234/source","title":["Synthetic lookup draft"],"author":[{"name":"Institute; Unit"}],"type":"journal-article"}}`)), Request: r}, nil
		})}, nil
	})
	before := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/items?limit=20", nil, owner)
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/metadata-lookup", map[string]any{"reference": "https://doi.org/10.1234/SOURCE"}, owner)
	var draft literaturediscovery.DOIMetadataDraft
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &draft) != nil || draft.DOI != "10.1234/source" || draft.Title == "" || calls != 1 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing read draft: HTTP%d", response.Code)
	}
	after := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/items?limit=20", nil, owner)
	if after.Body.String() != before.Body.String() {
		t.Fatal("lookup created catalogue state")
	}
	op := uuid.NewString()
	input := workspace.LiteratureManualCreate{OperationID: op, Title: draft.Title, Metadata: draft.Metadata, ProjectID: f.stream.ProjectID}
	created := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/items", input, owner)
	replayed := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/items", input, owner)
	var receipt workspace.LiteratureReceipt
	if created.Code != 200 || json.Unmarshal(created.Body.Bytes(), &receipt) != nil || receipt.OperationID != op || receipt.Item.ID == "" || receipt.Item.Metadata["doi"] != draft.DOI || created.Body.String() != replayed.Body.String() || calls != 1 {
		t.Fatal("original create/receipt authority was not reused")
	}
}

func TestLiteratureDOILookupContainsInvalidAndProviderFailures(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	owner := f.stream.OwnerID
	calls := 0
	f.server.doiMetadataLookup = literaturediscovery.NewDOIMetadataLookup(func(context.Context, string) (*http.Client, error) { calls++; return nil, nil })
	for _, tc := range []struct {
		method, path string
		body         any
		status       int
	}{
		{http.MethodGet, "/api/literature/metadata-lookup", nil, 405},
		{http.MethodPost, "/api/literature/metadata-lookup?url=https://other.test", map[string]any{"reference": "10.1234/source"}, 400},
		{http.MethodPost, "/api/literature/metadata-lookup", map[string]any{"reference": "10.1234/source", "operation_id": uuid.NewString()}, 400},
		{http.MethodPost, "/api/literature/metadata-lookup", map[string]any{"reference": "https://other.test/10.1234/source"}, 400},
	} {
		if got := p3JSONRequest(t, f.server, tc.method, tc.path, tc.body, owner); got.Code != tc.status {
			t.Fatalf("bad contract status:%d expected%d", got.Code, tc.status)
		}
	}
	if calls != 0 {
		t.Fatal("invalid query reached provider")
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/metadata-lookup", map[string]any{"reference": "10.1234/source"}, owner)
	if response.Code != 503 || calls != 1 {
		t.Fatal("provider failure was not a bounded read failure")
	}
}
