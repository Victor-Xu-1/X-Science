package literaturediscovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testSeedID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testChildID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type catalogFunc func(context.Context, string, string) (Reference, error)

func (fn catalogFunc) GetReference(ctx context.Context, owner, itemID string) (Reference, error) {
	return fn(ctx, owner, itemID)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func testReference() Reference {
	return Reference{ID: "local", Revision: 1, Title: "Seed", Identifiers: []Identifier{{Scheme: "doi", Value: "10.1234/seed"}}}
}
func testRequest() Request {
	return Request{ItemID: "local", ExpectedMetadataRevision: 1, Direction: "references"}
}
func testPaper(id string) map[string]any {
	return map[string]any{"paperId": id, "title": "Seed", "authors": []any{map[string]any{"name": "Full Unsplit Name"}}}
}
func testJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func testResponse(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func testFinder(t *testing.T, reference Reference, responses ...*http.Response) (*Finder, *[]*http.Request) {
	t.Helper()
	calls := []*http.Request{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls = append(calls, request.Clone(request.Context()))
		if len(responses) == 0 {
			t.Fatalf("unexpected provider request %s", request.URL)
			return nil, errors.New("unexpected request")
		}
		response := responses[0]
		responses = responses[1:]
		return response, nil
	})}
	finder := New(Options{
		Catalog: catalogFunc(func(_ context.Context, owner, itemID string) (Reference, error) {
			if owner != "owner" || itemID != reference.ID {
				return Reference{}, errors.New("not owned")
			}
			return reference, nil
		}),
		ClientForURL: func(_ context.Context, address string) (*http.Client, error) {
			if !strings.HasPrefix(address, providerOrigin) {
				t.Fatalf("untrusted URL passed to host factory: %s", address)
			}
			return client, nil
		},
		APIKey: func(context.Context, string) (string, error) { return "fixture-secret", nil },
	})
	// No clock waits are part of a controlled transport fixture.
	finder.requestInterval = 0
	return finder, &calls
}

func TestFinderExactIdentityNormalizesRecordAndPage(t *testing.T) {
	seed := testPaper(testSeedID)
	child := testPaper(strings.ToUpper(testChildID))
	child["abstract"], child["year"], child["publicationDate"] = " Body ", 2024, "2024-02-01"
	child["journal"] = map[string]any{"name": "Journal", "volume": "9", "pages": "10-20"}
	child["publicationTypes"] = []string{"Conference", "Review"}
	child["externalIds"] = map[string]any{"DOI": "https://doi.org/10.1234/CHILD", "PubMed": 12345, "PubMedCentral": "1234", "ArXiv": "2401.12345v2"}
	finder, calls := testFinder(t, testReference(), testResponse(testJSON(t, seed), 200),
		testResponse(testJSON(t, map[string]any{"offset": 0, "next": 10000, "data": []any{
			map[string]any{"citedPaper": child}, map[string]any{"citedPaper": child}, map[string]any{"citedPaper": nil}, map[string]any{},
		}}), 200))
	result, err := finder.Run(context.Background(), "owner", testRequest())
	if err != nil || result.State != "ready" || len(result.Records) != 1 || result.Omitted != 2 || result.NextOffset == nil || *result.NextOffset != 10000 {
		t.Fatalf("unexpected result: %#v / %v", result, err)
	}
	record := result.Records[0]
	if record.PaperID != testChildID || record.URL != "https://www.semanticscholar.org/paper/"+testChildID ||
		record.Authors[0] != "Full Unsplit Name" || record.ItemType != "review" || record.Abstract != "Body" ||
		record.Year == nil || *record.Year != 2024 || record.Volume != "9" || record.Pages != "10-20" || len(record.Identifiers) != 4 {
		t.Fatalf("bibliographic content lost: %#v", record)
	}
	if len(*calls) != 2 || (*calls)[0].URL.EscapedPath() != "/graph/v1/paper/DOI%3A10.1234%2Fseed" ||
		(*calls)[1].URL.Path != "/graph/v1/paper/"+testSeedID+"/references" ||
		(*calls)[1].URL.Query().Get("fields") != providerFields || (*calls)[1].URL.Query().Get("limit") != "20" ||
		(*calls)[0].Header.Get("x-api-key") != "fixture-secret" {
		t.Fatalf("unexpected provider sequence: %#v", *calls)
	}
	if strings.Contains(testJSON(t, result), "fixture-secret") {
		t.Fatal("credential leaked")
	}
	// A caller's result must not mutate the cached resolution used on later pages.
	result.Seed.Authors[0] = "mutated"
	if cached, ok := finder.resolutionFor("owner", Request{ItemID: "local", ExpectedMetadataRevision: 1, Direction: "references", ResolutionToken: result.ResolutionToken}); !ok || cached.seed.Authors[0] != "Full Unsplit Name" {
		t.Fatal("cached seed shares caller-owned slices")
	}
}

func TestFinderTitleMatchRequiresOwnerRevisionDirectionConfirmation(t *testing.T) {
	reference := testReference()
	reference.Identifiers = nil
	finder, calls := testFinder(t, reference,
		testResponse(testJSON(t, map[string]any{"data": []any{testPaper(testSeedID)}}), 200),
		testResponse(testJSON(t, map[string]any{"offset": 0, "data": []any{map[string]any{"citedPaper": testPaper(testChildID)}}}), 200))
	match, err := finder.Run(context.Background(), "owner", testRequest())
	if err != nil || match.State != "match-required" || match.ResolutionToken == "" || len(*calls) != 1 || len(match.Records) != 0 {
		t.Fatalf("title match was not explicit: %#v / %v", match, err)
	}
	if (*calls)[0].URL.Path != "/graph/v1/paper/search/match" || (*calls)[0].URL.Query().Get("query") != "Seed" {
		t.Fatal("not native title match")
	}
	request := testRequest()
	request.ResolutionToken = match.ResolutionToken
	wrongDirection := request
	wrongDirection.Direction = "citations"
	if result, _ := finder.Run(context.Background(), "owner", wrongDirection); result.Issue != "expired" || len(*calls) != 1 {
		t.Fatal("token crossed directions")
	}
	finder.options.Catalog = catalogFunc(func(context.Context, string, string) (Reference, error) { return reference, nil })
	if result, _ := finder.Run(context.Background(), "another-owner", request); result.Issue != "expired" || len(*calls) != 1 {
		t.Fatal("token crossed owners")
	}
	confirmed, err := finder.Run(context.Background(), "owner", request)
	if err != nil || confirmed.State != "ready" || len(confirmed.Records) != 1 || len(*calls) != 2 {
		t.Fatalf("confirmation failed: %#v / %v", confirmed, err)
	}
	request.ExpectedMetadataRevision = 2
	if result, _ := finder.Run(context.Background(), "owner", request); result.Issue != "reference-changed" || result.Seed != nil || result.ResolutionToken != "" {
		t.Fatal("stale reference retained private seed/token")
	}
}

func TestFinderReferenceRecheckDiscardsLateData(t *testing.T) {
	finder, calls := testFinder(t, testReference(), testResponse(testJSON(t, testPaper(testSeedID)), 200),
		testResponse(`{"offset":0,"data":[]}`, 200))
	reads := 0
	finder.options.Catalog = catalogFunc(func(context.Context, string, string) (Reference, error) {
		reads++
		if reads > 1 {
			return Reference{}, ErrReferenceUnavailable
		}
		return testReference(), nil
	})
	result, err := finder.Run(context.Background(), "owner", testRequest())
	if err != nil || result.Issue != "reference-changed" || result.State != "unavailable" || result.Seed != nil ||
		result.ResolutionToken != "" || len(result.Records) != 0 || len(finder.resolutions) != 0 || len(*calls) != 2 {
		t.Fatalf("late private data survived: %#v / %v", result, err)
	}
}

func TestFinderCatalogFailureIsNotReferenceChange(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure error
		want    string
	}{
		{"storage", errors.New("sqlite I/O private detail"), "unavailable"},
		{"missing-or-unauthorized", ErrReferenceUnavailable, "reference-changed"},
		{"catalog-timeout", context.DeadlineExceeded, "timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			finder, calls := testFinder(t, testReference())
			finder.options.Catalog = catalogFunc(func(context.Context, string, string) (Reference, error) { return Reference{}, test.failure })
			result, err := finder.Run(context.Background(), "owner", testRequest())
			if err != nil || result.Issue != test.want || result.State != "unavailable" || len(*calls) != 0 || strings.Contains(testJSON(t, result), "private detail") {
				t.Fatalf("catalog error misclassified: %#v / %v", result, err)
			}
		})
	}
}

func TestFinderTokenExpiryAndBoundedCache(t *testing.T) {
	finder := New(Options{})
	now := time.Unix(1000000, 0)
	finder.now = func() time.Time { return now }
	first := ""
	for index := 0; index <= resolutionLimit; index++ {
		token, err := newResolutionToken()
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			first = token
		}
		finder.rememberResolution(token, resolution{owner: "owner", itemID: "local", revision: 1, direction: "references", seed: Record{PaperID: testSeedID}, expires: now.Add(resolutionLifetime + time.Duration(index))})
	}
	if len(finder.resolutions) != resolutionLimit {
		t.Fatalf("unbounded token cache: %d", len(finder.resolutions))
	}
	if _, exists := finder.resolutions[first]; exists {
		t.Fatal("oldest token not evicted")
	}
	now = now.Add(resolutionLifetime + time.Second)
	if _, exists := finder.resolutionFor("owner", Request{ResolutionToken: first}); exists || len(finder.resolutions) != 0 {
		t.Fatal("expired tokens retained")
	}
}
