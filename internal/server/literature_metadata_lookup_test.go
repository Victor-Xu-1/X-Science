package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestLiteraturePMIDLookupBindsIdentityUntilManualCreate(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	owner := f.stream.OwnerID
	const reference = "https://pubmed.ncbi.nlm.nih.gov/12345/?source=fixture"
	providerCalls := 0
	f.server.doiMetadataLookup = literaturediscovery.NewDOIMetadataLookup(func(_ context.Context, endpoint string) (*http.Client, error) {
		const expectedURL = "https://www.ebi.ac.uk/europepmc/webservices/rest/search?format=json&pageSize=5&query=EXT_ID%3A12345+AND+SRC%3AMED&resultType=core"
		if endpoint != expectedURL {
			t.Errorf("PMID lookup did not use the fixed exact endpoint: %q", endpoint)
		}
		return &http.Client{Transport: publicationHTTPTransport(func(r *http.Request) (*http.Response, error) {
			providerCalls++
			pmid := "12345"
			if providerCalls == 1 {
				pmid = "12346"
			}
			body := `{"hitCount":1,"resultList":{"result":[{"id":"` + pmid + `","source":"MED","pmid":"` + pmid + `","title":"Synthetic PMID record","authorList":{"author":[{"firstName":"Ada","lastName":"Researcher"}]},"journalInfo":{"journal":{"title":"Synthetic journal"}}}]}}`
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}, nil
	})
	before := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/items?limit=20", nil, owner)
	lookup := func(reference string) *httptest.ResponseRecorder {
		return p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/metadata-lookup", map[string]any{
			"scheme": "pmid", "reference": reference,
		}, owner)
	}
	if got := lookup("https://example.test/12345"); got.Code != http.StatusBadRequest || providerCalls != 0 {
		t.Fatalf("untrusted URL reached provider: HTTP%d calls=%d", got.Code, providerCalls)
	}
	if got := lookup("PMID: 12345"); got.Code != http.StatusServiceUnavailable || providerCalls != 1 {
		t.Fatalf("mismatched PMID accepted: HTTP%d calls=%d", got.Code, providerCalls)
	}
	response := lookup(reference)
	var draft literaturediscovery.PMIDMetadataDraft
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &draft) != nil ||
		draft.Schema != "x-science.literature-pmid-draft.v1" || draft.Reference != reference ||
		draft.PMID != "12345" || draft.DOI != "" || draft.Metadata["pmid"] != draft.PMID ||
		draft.Metadata["doi"] != nil || providerCalls != 2 {
		t.Fatalf("invalid PMID-only draft: HTTP%d calls=%d body=%s", response.Code, providerCalls, response.Body)
	}
	after := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/items?limit=20", nil, owner)
	if before.Code != http.StatusOK || after.Code != http.StatusOK || before.Body.String() != after.Body.String() {
		t.Fatal("PMID lookup changed the catalogue before save")
	}
	input := workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: draft.Title, Metadata: draft.Metadata}
	saved := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/items", input, owner)
	var receipt workspace.LiteratureReceipt
	if saved.Code != http.StatusOK || json.Unmarshal(saved.Body.Bytes(), &receipt) != nil ||
		receipt.Item.ID == "" || receipt.Item.Metadata["pmid"] != draft.PMID {
		t.Fatalf("manual save dropped verified PMID: HTTP%d body=%s", saved.Code, saved.Body)
	}
	input.OperationID, input.Title = uuid.NewString(), "Do not overwrite matching item"
	repeated := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/items", input, owner)
	var duplicate workspace.LiteratureReceipt
	if repeated.Code != http.StatusOK || json.Unmarshal(repeated.Body.Bytes(), &duplicate) != nil ||
		duplicate.Item.ID != receipt.Item.ID || duplicate.Item.Title != draft.Title || providerCalls != 2 {
		t.Fatal("same PMID failed original owner-scoped deduplication")
	}
	reloaded, err := f.store.GetLiteratureItem(t.Context(), owner, receipt.Item.ID)
	if err != nil || reloaded.Metadata["pmid"] != draft.PMID {
		t.Fatalf("saved PMID did not survive readback: %v", err)
	}
}
