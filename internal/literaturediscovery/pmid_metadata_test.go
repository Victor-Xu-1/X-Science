package literaturediscovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestPMIDMetadataLookupChecksEuropePMCExactIdentity(t *testing.T) {
	const med = `{"source":"MED","id":"12345","pmid":"12345","title":"<b>Exact &amp; safe</b>","doi":"10.1234/EXACT","firstPublicationDate":"2022-11-01","abstractText":"<p>Evidence</p>","pageInfo":"1-7","journalInfo":{"volume":"9","journal":{"title":"Research Journal"}},"authorList":{"author":[{"firstName":"Ada","lastName":"Researcher"},{"collectiveName":"Research Consortium"},{"fullName":"Other Author"}]}}`
	for _, tt := range []struct {
		name string
		body string
		want error
	}{
		{"exact", `{"hitCount":1,"resultList":{"result":[` + med + `]}}`, nil},
		{"empty", `{"hitCount":0,"resultList":{"result":[]}}`, ErrDOIMetadataNotFound},
		{"missing-count", `{"resultList":{"result":[` + med + `]}}`, ErrDOIMetadataUnavailable},
		{"missing-results", `{"hitCount":0,"resultList":{}}`, ErrDOIMetadataUnavailable},
		{"wrong-source", `{"hitCount":1,"resultList":{"result":[{"source":"PMC","id":"12345","title":"Same number"}]}}`, ErrDOIMetadataUnavailable},
		{"contradictory-pmid", `{"hitCount":1,"resultList":{"result":[{"source":"MED","id":"12345","pmid":"12346","title":"Contradictory"}]}}`, ErrDOIMetadataUnavailable},
		{"duplicate", `{"hitCount":2,"resultList":{"result":[` + med + `,` + med + `]}}`, ErrDOIMetadataUnavailable},
		{"truncated-page", `{"hitCount":6,"resultList":{"result":[` + med + `]}}`, ErrDOIMetadataUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			lookup := NewDOIMetadataLookup(func(_ context.Context, endpoint string) (*http.Client, error) {
				if !strings.HasPrefix(endpoint, "https://www.ebi.ac.uk/europepmc/webservices/rest/search?") {
					t.Fatalf("unexpected provider: %q", endpoint)
				}
				return &http.Client{Transport: doiMetadataTransport(func(request *http.Request) (*http.Response, error) {
					calls++
					if request.Method != http.MethodGet || request.Header.Get("Accept") != "application/json" ||
						request.Header.Get("Authorization") != "" || request.Header.Get("x-api-key") != "" {
						t.Fatalf("unexpected PMID request: %#v", request)
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
				})}, nil
			})
			draft, err := lookup.LookupPMID(t.Context(), "PMID:12345")
			if !errors.Is(err, tt.want) || calls != 1 {
				t.Fatalf("PMID result=%#v err=%v calls=%d, want %v", draft, err, calls, tt.want)
			}
			if tt.want == nil && (draft.Title != "Exact & safe" || draft.PMID != "12345" || draft.DOI != "10.1234/exact" ||
				draft.Metadata["year"] != "2022" || draft.Metadata["container_title"] != "Research Journal" ||
				draft.Metadata["volume"] != "9" || draft.Metadata["pages"] != "1-7" || draft.Metadata["abstract"] != "Evidence" ||
				!reflect.DeepEqual(draft.Metadata["authors"], []string{"Ada Researcher", "Research Consortium", "Other Author"})) {
				t.Fatalf("structured metadata not preserved: %#v", draft)
			}
		})
	}
}
