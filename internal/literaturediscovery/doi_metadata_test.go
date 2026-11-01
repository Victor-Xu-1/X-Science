package literaturediscovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

const doiMetadataFixture = `{"status":"ok","message-type":"work","message":{"DOI":"10.1234/SOURCE","type":"journal-article","title":["A <i>synthetic</i> reference"],"author":[{"given":"Ada","family":"Researcher"},{"name":"Institute; Unit"}],"published":{"date-parts":[[2024,2,1]]},"container-title":["Synthetic journal"],"publisher":"Fixture publisher","abstract":"<jats:p>Controlled &amp; reproducible.</jats:p>"}}`

type doiMetadataTransport func(*http.Request) (*http.Response, error)

func (transport doiMetadataTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return transport(r)
}

func TestDOIMetadataCanonicalDraftAndControlledTransport(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	provider, _ := url.Parse("https://api.crossref.org")
	jar.SetCookies(provider, []*http.Cookie{{Name: "private-cookie", Value: "must-not-leave"}})
	calls := 0
	lookup := NewDOIMetadataLookup(func(ctx context.Context, raw string) (*http.Client, error) {
		if raw != "https://api.crossref.org/works/10.1234%2Fsource" {
			t.Fatal("untrusted provider route")
		}
		return &http.Client{Jar: jar, Transport: doiMetadataTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/json" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
				t.Fatal("private state crossed public read")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(doiMetadataFixture)), Request: r}, nil
		})}, nil
	})
	for _, input := range []string{"doi: 10.1234/SOURCE", "https://doi.org/10.1234%2FSOURCE?browser=1#section"} {
		draft, err := lookup.Lookup(t.Context(), input)
		if err != nil || draft.Reference != input || draft.DOI != "10.1234/source" || draft.Title != "A synthetic reference" || draft.Metadata["year"] != "2024" || draft.Metadata["abstract"] != "Controlled & reproducible." {
			t.Fatalf("bad draft: %#v %v", draft, err)
		}
		if authors, ok := draft.Metadata["authors"].([]string); !ok || len(authors) != 2 || authors[1] != "Institute; Unit" {
			t.Fatal("creator identities changed")
		}
	}
	if calls != 2 {
		t.Fatal("lookup retried or did not run")
	}
}

func TestDOIMetadataRejectsUnsupportedIdentifiersBeforeIO(t *testing.T) {
	lookup := NewDOIMetadataLookup(func(context.Context, string) (*http.Client, error) {
		t.Fatal("invalid input reached provider")
		return nil, nil
	})
	for _, input := range []string{"", "arxiv:2401.12345", "https://example.test/10.1234/source", "https://user@doi.org/10.1234/source", "https://doi.org:8443/10.1234/source", "10.1234/a\x00b", strings.Repeat("x", 8193)} {
		if _, err := lookup.Lookup(t.Context(), input); !errors.Is(err, ErrDOIMetadataInvalid) {
			t.Fatalf("accepted %q: %v", input, err)
		}
	}
}

func TestDOIMetadataRejectsMismatchedOrUnavailableProviderResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"wrong-doi", 200, strings.Replace(doiMetadataFixture, "10.1234/SOURCE", "10.1234/other", 1), ErrDOIMetadataUnavailable},
		{"missing-title", 200, `{"status":"ok","message-type":"work","message":{"DOI":"10.1234/source"}}`, ErrDOIMetadataUnavailable},
		{"trailing-json", 200, doiMetadataFixture + `{}`, ErrDOIMetadataUnavailable},
		{"oversize", 200, strings.Repeat(" ", (1<<20)+1), ErrDOIMetadataUnavailable},
		{"redirect", 302, "", ErrDOIMetadataUnavailable},
		{"not-found", 404, "", ErrDOIMetadataNotFound},
		{"limited", 429, "", ErrDOIMetadataRateLimited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			lookup := NewDOIMetadataLookup(func(context.Context, string) (*http.Client, error) {
				return &http.Client{Transport: doiMetadataTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"https://other.test"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
				})}, nil
			})
			if _, err := lookup.Lookup(t.Context(), "10.1234/source"); !errors.Is(err, tc.want) || calls != 1 {
				t.Fatalf("provider read not contained: %v calls%d", err, calls)
			}
		})
	}
}

func TestDOIMetadataCancellationCannotReturnDraft(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	lookup := NewDOIMetadataLookup(func(context.Context, string) (*http.Client, error) {
		return &http.Client{Transport: doiMetadataTransport(func(r *http.Request) (*http.Response, error) {
			cancel()
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(doiMetadataFixture)), Request: r}, nil
		})}, nil
	})
	if draft, err := lookup.Lookup(ctx, "10.1234/source"); !errors.Is(err, context.Canceled) || draft.Title != "" {
		t.Fatal("cancelled proposal was accepted", err)
	}
}

func TestDOIMetadataKnownCrossrefTypes(t *testing.T) {
	for _, tc := range []struct {
		provider, expected string
	}{
		{"edited-book", "book"},
		{"reference-book", "book"},
		{"book-part", "bookSection"},
		{"book-section", "bookSection"},
		{"reference-entry", "bookSection"},
		{"dissertation", "thesis"},
		{"report", "report"},
		{"report-series", "report"},
		{"standard", "standard"},
		{"standard-series", "standard"},
		{"journal-article", "journalArticle"},
		{"book-chapter", "bookSection"},
		{"unregistered-work-type", "document"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			calls := 0
			body := strings.Replace(doiMetadataFixture, `"type":"journal-article"`, `"type":"`+tc.provider+`"`, 1)
			lookup := NewDOIMetadataLookup(func(context.Context, string) (*http.Client, error) {
				return &http.Client{Transport: doiMetadataTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}, nil
			})
			draft, err := lookup.Lookup(t.Context(), "10.1234/source")
			if err != nil || draft.Metadata["item_type"] != tc.expected || calls != 1 {
				t.Fatalf("Crossref type %s: got %#v, err=%v calls=%d; want %s", tc.provider, draft.Metadata["item_type"], err, calls, tc.expected)
			}
		})
	}
}
