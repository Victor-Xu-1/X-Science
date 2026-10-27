package literaturediscovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type publicationRoundTrip func(*http.Request) (*http.Response, error)

func (run publicationRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func publicationFixture(status int, body string) *PublicationChecker {
	return NewPublicationChecker(func(_ context.Context, raw string) (*http.Client, error) {
		return &http.Client{Transport: publicationRoundTrip(func(request *http.Request) (*http.Response, error) {
			if raw != "https://api.crossref.org/works/10.1234%2Fsource" || request.Method != "GET" || request.Header.Get("Accept") != "application/json" {
				return nil, errors.New("unexpected publication request")
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})}, nil
	})
}

func TestPublicationCheckUsesReverseEdgesAndPreservesProviderRecords(t *testing.T) {
	checker := publicationFixture(200, `{"status":"ok","message-type":"work","message":{"DOI":"10.1234/SOURCE","updated-by":[{"DOI":"10.1234/notice","type":"correction","source":"publisher","updated":{"date-parts":[[2024,2,29]]}},{"DOI":"10.1234/notice","type":"correction","updated":{"date-parts":[[2024,2]]}}],"update-to":[{"DOI":"10.1234/unrelated","type":"retraction"}]}}`)
	checker.now = func() time.Time { return time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC) }
	snapshot, issue, err := checker.Check(context.Background(), "10.1234/source")
	if err != nil || issue != "" || snapshot.DOI != "10.1234/source" || len(snapshot.Notices) != 2 {
		t.Fatalf("wrong source check: %#v %s %v", snapshot, issue, err)
	}
	if snapshot.Notices[0].Date != "2024-02-29" || snapshot.Notices[1].Date != "" || snapshot.Notices[1].Source != "crossref" {
		t.Fatalf("provider records or partial dates lost: %#v", snapshot.Notices)
	}
}

func TestPublicationCheckRejectsWrongSourceInvalidBodyAndRedirect(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		issue  PublicationIssue
	}{
		{"wrong-source", 200, `{"status":"ok","message-type":"work","message":{"DOI":"10.1234/other"}}`, PublicationResponseInvalid},
		{"wrong-shape", 200, `{"status":"ok","message-type":"work","message":{"DOI":"10.1234/source","updated-by":[{"DOI":"10.1234/notice","type":"correction","source":"unsupported"}]}}`, PublicationResponseInvalid},
		{"trailing-body", 200, `{"status":"ok","message-type":"work","message":{"DOI":"10.1234/source"}} {}`, PublicationResponseInvalid},
		{"oversize", 200, strings.Repeat(" ", publicationMaxBytes+1), PublicationResponseInvalid},
		{"redirect", 302, "", PublicationUnavailable},
		{"not-found", 404, "", PublicationNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, issue, err := publicationFixture(tc.status, tc.body).Check(context.Background(), "10.1234/source")
			if err != nil || issue != tc.issue || snapshot.DOI != "" {
				t.Fatalf("%#v %s %v", snapshot, issue, err)
			}
		})
	}
}

func TestPublicationCheckCancellationAndRateLimitDoNotProduceSnapshots(t *testing.T) {
	calls := 0
	checker := NewPublicationChecker(func(ctx context.Context, raw string) (*http.Client, error) {
		calls++
		return &http.Client{Transport: publicationRoundTrip(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"2"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		})}, nil
	})
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	checker.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if snapshot, _, err := checker.Check(ctx, "10.1234/source"); !errors.Is(err, context.Canceled) || snapshot.DOI != "" || calls != 0 {
		t.Fatalf("cancelled caller reached source: %#v %v calls%d", snapshot, err, calls)
	}
	for i := 0; i < 2; i++ {
		if snapshot, issue, err := checker.Check(context.Background(), "10.1234/source"); err != nil || issue != PublicationRateLimited || snapshot.DOI != "" {
			t.Fatalf("rate limit returned evidence: %#v %s %v", snapshot, issue, err)
		}
	}
	if calls != 1 {
		t.Fatalf("rate limit was ignored: %d", calls)
	}
	now = now.Add(3 * time.Second)
	_, _, _ = checker.Check(context.Background(), "10.1234/source")
	if calls != 2 {
		t.Fatalf("bounded retry time did not expire: %d", calls)
	}
}

func TestPublicationDOICanonicalizationPreservesBalancedIdentity(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{" DOI: https://doi.org/10.1234/ABC(foo). ", "10.1234/abc(foo)"},
		{"10.1234/ABC(foo))", "10.1234/abc(foo)"},
		{"10.1234/a[b]", "10.1234/a[b]"},
		{"10.1234/invalid\nvalue", ""},
		{"10.12/short", ""},
		{"https://example.org/10.1234/source", ""},
	} {
		got, valid := NormalizeDOI(tc.raw)
		if got != tc.want || valid != (tc.want != "") {
			t.Fatalf("%q => %q/%v; want%q", tc.raw, got, valid, tc.want)
		}
	}
}

func TestPublicationCheckClampsLongProviderCooldownBeforeRetry(t *testing.T) {
	calls := 0
	checker := NewPublicationChecker(func(context.Context, string) (*http.Client, error) {
		calls++
		return &http.Client{Transport: publicationRoundTrip(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"600"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		})}, nil
	})
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	checker.now = func() time.Time { return now }
	for _, seconds := range []int{0, 120, 181} {
		now = now.Add(time.Duration(seconds) * time.Second)
		snapshot, issue, err := checker.Check(context.Background(), "10.1234/source")
		if err != nil || issue != PublicationRateLimited || snapshot.DOI != "" {
			t.Fatalf("rate-limited request produced evidence: %#v %s %v", snapshot, issue, err)
		}
		wantCalls := 1
		if seconds == 181 {
			wantCalls = 2
		}
		if calls != wantCalls {
			t.Fatalf("bounded provider cooldown ignored: got%d want%d", calls, wantCalls)
		}
	}
}
