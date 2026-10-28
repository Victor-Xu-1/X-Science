package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"x-science/internal/literaturediscovery"
	secretstore "x-science/internal/persistence/secrets"
	workspace "x-science/internal/persistence/workspace"
)

const (
	relatedTestOwner  = "related-owner"
	relatedTestSeed   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	relatedTestChild  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	relatedTestFields = "title,abstract,authors,year,venue,externalIds,publicationDate,journal,publicationTypes"
)

type literatureRelatedRoundTripFunc func(*http.Request) (*http.Response, error)

func (run literatureRelatedRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func newLiteratureRelatedHTTPFixture(t *testing.T, metadata map[string]any) (*Server, workspace.LiteratureReceipt) {
	t.Helper()
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err = store.CreateProject(workspace.CreateProjectInput{ID: "related-source", UserID: relatedTestOwner, Name: "Synthetic source"}); err != nil {
		t.Fatal(err)
	}
	// This tests the real Library owner/store, not PDF inspection or scientific
	// retrieval. PageCount is explicitly supplied synthetic import evidence.
	artifact, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: "related-paper", ProjectID: "related-source", Name: "synthetic.pdf", Kind: "application/pdf",
		Content: []byte("%PDF-1.4\nsynthetic-bibliographic-fixture\n%%EOF"),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ImportLiteratureArtifact(t.Context(), relatedTestOwner, workspace.LiteratureImport{
		OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID,
		Title: "Synthetic seed", Metadata: metadata, PageCount: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{FileRoot: root, Workspace: store}), receipt
}

func injectLiteratureRelatedTransport(t *testing.T, app *Server, roundTrip literatureRelatedRoundTripFunc) {
	t.Helper()
	app.literatureRelatedOnce.Do(func() {
		app.literatureRelated = literaturediscovery.New(literaturediscovery.Options{
			Catalog: libraryRelatedCatalog{store: app.workspaceStore}, APIKey: app.relatedLiteratureAPIKey,
			ClientForURL: func(ctx context.Context, endpoint string) (*http.Client, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if !strings.HasPrefix(endpoint, "https://api.semanticscholar.org/graph/v1/") {
					t.Fatalf("untrusted endpoint reached the controlled factory: %q", endpoint)
				}
				return &http.Client{Transport: roundTrip}, nil
			},
		})
	})
}

func literatureRelatedResponse(request *http.Request, status int, payload any) (*http.Response, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(raw))), Request: request}, nil
}

func literatureRelatedQuery(item workspace.LiteratureItem, direction string) literaturediscovery.Request {
	return literaturediscovery.Request{ItemID: item.ID, ExpectedMetadataRevision: item.MetadataRevision, Direction: direction}
}

func decodeRelatedHTTP(t *testing.T, response *httptest.ResponseRecorder) literaturediscovery.Result {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("related HTTP%d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("related response became cacheable across owners")
	}
	var result literaturediscovery.Result
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != "x-science.literature-related.v1" || result.Records == nil {
		t.Fatalf("invalid typed response: %#v", result)
	}
	return result
}

func TestLiteratureRelatedCatalogUsesRealOwnerAndMetadataRevision(t *testing.T) {
	app, imported := newLiteratureRelatedHTTPFixture(t, map[string]any{
		"identifiers": []any{map[string]any{"scheme": "doi", "value": "10.1234/seed"},
			map[string]any{"scheme": "unsupported", "value": "foreign.invalid"}},
		"pmid": "12345", "semantic_scholar_id": relatedTestSeed,
		"provider_url": "https://foreign.invalid/not-authority", "candidate_accepted": true,
	})
	catalog := libraryRelatedCatalog{store: app.workspaceStore}
	reference, err := catalog.GetReference(t.Context(), relatedTestOwner, imported.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reference.ID != imported.Item.ID || reference.Revision != 1 || reference.Title != imported.Item.Title || reference.SemanticScholarID != relatedTestSeed {
		t.Fatalf("reference lost real Library identity: %#v", reference)
	}
	if !reflect.DeepEqual(reference.Identifiers, []literaturediscovery.Identifier{{Scheme: "doi", Value: "10.1234/seed"}, {Scheme: "pmid", Value: "12345"}}) {
		t.Fatalf("unsupported metadata became source authority: %#v", reference.Identifiers)
	}
	if _, err = catalog.GetReference(t.Context(), "other-owner", imported.Item.ID); !errors.Is(err, literaturediscovery.ErrReferenceUnavailable) {
		t.Fatalf("foreign catalog read=%v", err)
	}
	title := "Changed synthetic seed"
	changed, err := app.workspaceStore.PatchLiteratureItem(t.Context(), relatedTestOwner, imported.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	reference, err = catalog.GetReference(t.Context(), relatedTestOwner, imported.Item.ID)
	if err != nil || reference.Revision != changed.Item.MetadataRevision || reference.Revision != 2 || reference.Title != title {
		t.Fatalf("catalog used stale metadata: %#v %v", reference, err)
	}
	deleted := true
	if _, err = app.workspaceStore.PatchLiteratureItem(t.Context(), relatedTestOwner, imported.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 2, Deleted: &deleted}); err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.GetReference(t.Context(), relatedTestOwner, imported.Item.ID); !errors.Is(err, literaturediscovery.ErrReferenceUnavailable) {
		t.Fatalf("tombstone catalog read=%v", err)
	}
}

func TestLiteratureRelatedHTTPUsesExactProviderQueryAndOwnerCredential(t *testing.T) {
	for _, direction := range []string{"references", "citations"} {
		t.Run(direction, func(t *testing.T) {
			app, imported := newLiteratureRelatedHTTPFixture(t, map[string]any{"doi": "10.1234/seed"})
			for _, secret := range []secretstore.Secret{
				{ID: "owned-related", UserID: relatedTestOwner, Provider: "literature", Credentials: map[string]string{"semantic_scholar_api_key": "synthetic-owned-key"}},
				{ID: "foreign-related", UserID: "other-owner", Provider: "literature", Credentials: map[string]string{"semantic_scholar_api_key": "synthetic-foreign-key"}},
			} {
				if _, err := app.secretStore.Create(secret); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			injectLiteratureRelatedTransport(t, app, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "api.semanticscholar.org" || request.URL.Query().Get("fields") != relatedTestFields {
					t.Fatalf("wrong provider query: %s %s", request.Method, request.URL)
				}
				if request.Header.Get("x-api-key") != "synthetic-owned-key" || request.Header.Get("Cookie") != "" {
					t.Fatal("provider credential owner/cookie boundary changed")
				}
				if calls == 1 {
					if request.URL.EscapedPath() != "/graph/v1/paper/DOI%3A10.1234%2Fseed" {
						t.Fatalf("wrong exact seed path: %s", request.URL.EscapedPath())
					}
					return literatureRelatedResponse(request, 200, map[string]any{"paperId": relatedTestSeed, "title": "Synthetic seed"})
				}
				if calls != 2 || request.URL.Path != "/graph/v1/paper/"+relatedTestSeed+"/"+direction || request.URL.Query().Get("limit") != "20" || request.URL.Query().Get("offset") != "0" {
					t.Fatalf("wrong relationship page: %s", request.URL)
				}
				key := "citedPaper"
				if direction == "citations" {
					key = "citingPaper"
				}
				return literatureRelatedResponse(request, 200, map[string]any{"offset": 0, "data": []any{
					map[string]any{key: map[string]any{"paperId": relatedTestChild, "title": "Synthetic child", "authors": []any{map[string]any{"name": "One unsplit author"}}, "externalIds": map[string]any{"DOI": "10.1234/child"}}}, map[string]any{key: nil},
				}})
			})
			response := p3JSONRequest(t, app, http.MethodPost, "/api/literature/related", literatureRelatedQuery(imported.Item, direction), relatedTestOwner)
			result := decodeRelatedHTTP(t, response)
			if calls != 2 || result.State != "ready" || result.ItemID != imported.Item.ID || result.MetadataRevision != 1 || result.Direction != direction || len(result.Records) != 1 || result.Omitted != 1 {
				t.Fatalf("related result lost source/direction/omissions: %#v calls=%d", result, calls)
			}
			if result.Records[0].PaperID != relatedTestChild || !reflect.DeepEqual(result.Records[0].Authors, []string{"One unsplit author"}) || result.Records[0].URL != "https://www.semanticscholar.org/paper/"+relatedTestChild {
				t.Fatalf("invalid bibliographic record: %#v", result.Records[0])
			}
			if strings.Contains(response.Body.String(), "synthetic-owned-key") || strings.Contains(response.Body.String(), "synthetic-foreign-key") {
				t.Fatal("credential leaked into public result")
			}
		})
	}
}

func TestLiteratureRelatedHTTPRejectsInvalidMissingForeignAndStaleBeforeNetwork(t *testing.T) {
	app, imported := newLiteratureRelatedHTTPFixture(t, map[string]any{"doi": "10.1234/seed"})
	calls := 0
	injectLiteratureRelatedTransport(t, app, func(request *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("controlled transport must not run")
	})
	valid := literatureRelatedQuery(imported.Item, "references")
	for _, input := range []any{
		map[string]any{"item_id": imported.Item.ID, "expected_metadata_revision": 1, "direction": "references", "offset": 0, "provider_url": "https://foreign.invalid"},
		literaturediscovery.Request{ItemID: imported.Item.ID, ExpectedMetadataRevision: 1, Direction: "unsupported"},
		literaturediscovery.Request{ItemID: imported.Item.ID, Direction: "references"},
		literaturediscovery.Request{ItemID: imported.Item.ID, ExpectedMetadataRevision: 1, Direction: "references", Offset: -1},
	} {
		response := p3JSONRequest(t, app, http.MethodPost, "/api/literature/related", input, relatedTestOwner)
		if response.Code != 400 || p3DecodeObject(t, response)["code"] != "LITERATURE_INVALID" {
			t.Fatalf("invalid caller accepted: HTTP%d %s", response.Code, response.Body.String())
		}
	}
	if response := p3JSONRequest(t, app, http.MethodPost, "/api/literature/related?ignored=1", valid, relatedTestOwner); response.Code != 400 {
		t.Fatalf("unexpected query accepted=%d", response.Code)
	}
	for _, denied := range []struct{ owner, item string }{{"other-owner", imported.Item.ID}, {relatedTestOwner, "missing-item"}} {
		request := valid
		request.ItemID = denied.item
		result := decodeRelatedHTTP(t, p3JSONRequest(t, app, http.MethodPost, "/api/literature/related", request, denied.owner))
		if result.State != "unavailable" || result.Issue != "reference-changed" || len(result.Records) != 0 || result.Seed != nil {
			t.Fatalf("unauthorized/missing source disclosed result: %#v", result)
		}
	}
	stale := valid
	stale.ExpectedMetadataRevision = 2
	result := decodeRelatedHTTP(t, p3JSONRequest(t, app, http.MethodPost, "/api/literature/related", stale, relatedTestOwner))
	if result.State != "unavailable" || result.Issue != "reference-changed" || len(result.Records) != 0 {
		t.Fatalf("stale reference accepted: %#v", result)
	}
	if calls != 0 {
		t.Fatalf("rejected caller/owner/revision issued %d provider requests", calls)
	}
}

func TestLiteratureRelatedHTTPPreservesTypedProviderFailureWithoutRawBody(t *testing.T) {
	for _, test := range []struct {
		status int
		issue  string
	}{{401, "authentication"}, {403, "authentication"}, {404, "no-match"}, {429, "rate-limited"}, {500, "unavailable"}, {200, "response-invalid"}} {
		t.Run(test.issue+"-"+http.StatusText(test.status), func(t *testing.T) {
			app, imported := newLiteratureRelatedHTTPFixture(t, map[string]any{"doi": "10.1234/seed"})
			calls := 0
			injectLiteratureRelatedTransport(t, app, func(request *http.Request) (*http.Response, error) {
				calls++
				response, err := literatureRelatedResponse(request, test.status, map[string]any{"error": "synthetic-private-provider-body"})
				response.Header.Set("Retry-After", "2")
				return response, err
			})
			response := p3JSONRequest(t, app, http.MethodPost, "/api/literature/related", literatureRelatedQuery(imported.Item, "references"), relatedTestOwner)
			result := decodeRelatedHTTP(t, response)
			if calls != 1 || result.State != "unavailable" || result.Issue != test.issue || len(result.Records) != 0 {
				t.Fatalf("provider failure became empty success or wrong issue: %#v calls=%d", result, calls)
			}
			if test.status == 429 && result.RetryAt <= 0 {
				t.Fatal("rate limit lost bounded retry time")
			}
			if strings.Contains(response.Body.String(), "synthetic-private-provider-body") {
				t.Fatal("raw provider failure leaked")
			}
		})
	}
}

func TestLiteratureRelatedHTTPDiscardsResultsAfterStoredMetadataChanges(t *testing.T) {
	app, imported := newLiteratureRelatedHTTPFixture(t, map[string]any{"doi": "10.1234/seed"})
	calls := 0
	injectLiteratureRelatedTransport(t, app, func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return literatureRelatedResponse(request, 200, map[string]any{"paperId": relatedTestSeed, "title": "Synthetic seed"})
		}
		title := "Edited while provider was reading"
		if _, err := app.workspaceStore.PatchLiteratureItem(t.Context(), relatedTestOwner, imported.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Title: &title}); err != nil {
			t.Fatal(err)
		}
		return literatureRelatedResponse(request, 200, map[string]any{"offset": 0, "data": []any{map[string]any{"citedPaper": map[string]any{"paperId": relatedTestChild, "title": "Old revision result"}}}})
	})
	result := decodeRelatedHTTP(t, p3JSONRequest(t, app, http.MethodPost, "/api/literature/related", literatureRelatedQuery(imported.Item, "references"), relatedTestOwner))
	if calls != 2 || result.State != "unavailable" || result.Issue != "reference-changed" || len(result.Records) != 0 || result.ResolutionToken != "" {
		t.Fatalf("late old-revision data escaped: %#v calls=%d", result, calls)
	}
	live, err := app.workspaceStore.GetLiteratureItem(t.Context(), relatedTestOwner, imported.Item.ID)
	if err != nil || live.MetadataRevision != 2 {
		t.Fatalf("test did not establish a real committed revision change: %#v %v", live, err)
	}
}

func TestLiteratureRelatedHTTPCancellationDoesNotPublishReadyResult(t *testing.T) {
	app, imported := newLiteratureRelatedHTTPFixture(t, map[string]any{"doi": "10.1234/seed"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls, canceled := 0, false
	injectLiteratureRelatedTransport(t, app, func(request *http.Request) (*http.Response, error) {
		calls++
		cancel()
		<-request.Context().Done()
		canceled = errors.Is(request.Context().Err(), context.Canceled)
		return nil, request.Context().Err()
	})
	raw, err := json.Marshal(literatureRelatedQuery(imported.Item, "references"))
	if err != nil {
		t.Fatal(err)
	}
	request := newLoopbackTestRequest(http.MethodPost, "/api/literature/related", strings.NewReader(string(raw))).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", relatedTestOwner)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if calls != 1 || !canceled || response.Body.Len() != 0 {
		t.Fatalf("cancellation emitted a successful projection: calls=%d canceled=%t body=%s", calls, canceled, response.Body.String())
	}
}

func TestLiteratureRelatedAPIKeyFiltersOwnerProviderAndAmbiguousValues(t *testing.T) {
	app := &Server{secretStore: secretstore.New(t.TempDir())}
	for _, secret := range []secretstore.Secret{
		{ID: "foreign", UserID: "other-owner", Provider: "literature", Credentials: map[string]string{"semantic_scholar_api_key": "synthetic-foreign"}},
		{ID: "irrelevant", UserID: relatedTestOwner, Provider: "openai", Credentials: map[string]string{"semantic_scholar_api_key": "synthetic-unrelated"}},
		{ID: "blank", UserID: relatedTestOwner, Provider: "literature", Credentials: map[string]string{"unused": "synthetic-unused"}},
		{ID: "owned", UserID: relatedTestOwner, Provider: "literature", Credentials: map[string]string{"semantic_scholar_api_key": " synthetic-owned "}},
	} {
		if _, err := app.secretStore.Create(secret); err != nil {
			t.Fatal(err)
		}
	}
	if key, err := app.relatedLiteratureAPIKey(t.Context(), relatedTestOwner); err != nil || key != "synthetic-owned" {
		t.Fatalf("owner key selection mismatch: empty=%t err=%v", key == "", err)
	}
	if key, err := app.relatedLiteratureAPIKey(t.Context(), "no-credentials"); err != nil || key != "" {
		t.Fatalf("foreign key became anonymous authority: empty=%t err=%v", key == "", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := app.relatedLiteratureAPIKey(ctx, relatedTestOwner); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled key lookup=%v", err)
	}
	if _, err := app.secretStore.Create(secretstore.Secret{ID: "ambiguous", UserID: relatedTestOwner, Provider: "literature", Credentials: map[string]string{"semantic_scholar_api_key": "synthetic-different"}}); err != nil {
		t.Fatal(err)
	}
	if key, err := app.relatedLiteratureAPIKey(t.Context(), relatedTestOwner); err == nil || key != "" {
		t.Fatal("ambiguous owner keys silently selected")
	}
}
