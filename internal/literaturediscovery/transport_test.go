package literaturediscovery

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type trackedBody struct {
	*strings.Reader
	closed bool
}

func (body *trackedBody) Close() error { body.closed = true; return nil }

type fixtureJar struct{}

func (fixtureJar) Cookies(*url.URL) []*http.Cookie {
	return []*http.Cookie{{Name: "ambient", Value: "private-cookie"}}
}
func (fixtureJar) SetCookies(*url.URL, []*http.Cookie) {}

func TestFinderClosedHTTPFailuresAndResponseCleanup(t *testing.T) {
	for _, test := range []struct {
		status int
		issue  string
	}{{401, "authentication"}, {403, "authentication"}, {404, "no-match"}, {503, "unavailable"}} {
		t.Run(test.issue+http.StatusText(test.status), func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader("private upstream response")}
			finder, calls := testFinder(t, testReference(), &http.Response{StatusCode: test.status, Body: body, Header: make(http.Header)})
			result, err := finder.Run(context.Background(), "owner", testRequest())
			if err != nil || result.State != "unavailable" || result.Issue != test.issue || len(result.Records) != 0 || result.Seed != nil || !body.closed || len(*calls) != 1 {
				t.Fatalf("incorrect closed failure: %#v / %v", result, err)
			}
			if strings.Contains(testJSON(t, result), "private upstream") {
				t.Fatal("upstream body leaked")
			}
		})
	}
}

func TestFinderBoundsBodyAndRejectsMalformedIdentifiedRows(t *testing.T) {
	for _, test := range []struct{ name, seed, page string }{
		{"oversized", strings.Repeat("x", providerBodyLimit+1), ""},
		{"invalid-json", "{", ""},
		{"bad-paper-id", `{"paperId":"../../secret","title":"Unsafe"}`, ""},
		{"bad-identified-child", testJSON(t, testPaper(testSeedID)), `{"offset":0,"data":[{"citedPaper":{"paperId":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","title":3}}]}`},
		{"invalid-row", testJSON(t, testPaper(testSeedID)), `{"offset":0,"data":[null]}`},
		{"regressing-page", testJSON(t, testPaper(testSeedID)), `{"offset":0,"next":0,"data":[]}`},
		{"wrong-offset", testJSON(t, testPaper(testSeedID)), `{"offset":1,"data":[]}`},
		{"missing-data", testJSON(t, testPaper(testSeedID)), `{"offset":0}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			responses := []*http.Response{testResponse(test.seed, 200)}
			if test.page != "" {
				responses = append(responses, testResponse(test.page, 200))
			}
			finder, _ := testFinder(t, testReference(), responses...)
			result, err := finder.Run(context.Background(), "owner", testRequest())
			if err != nil || result.Issue != "response-invalid" || result.State != "unavailable" || result.Seed != nil || len(result.Records) != 0 {
				t.Fatalf("malformed provider content accepted: %#v / %v", result, err)
			}
		})
	}
}

func TestFinderCooldownBoundToCredentialAndRetryAfter(t *testing.T) {
	limited := testResponse("secret error", 429)
	limited.Header.Set("Retry-After", "9999999")
	finder, calls := testFinder(t, testReference(), limited,
		testResponse(testJSON(t, testPaper(testSeedID)), 200), testResponse(`{"offset":0,"data":[]}`, 200))
	key := "old-key"
	finder.options.APIKey = func(context.Context, string) (string, error) { return key, nil }
	now := time.Unix(1000000, 0)
	finder.now = func() time.Time { return now }
	first, err := finder.Run(context.Background(), "owner", testRequest())
	if err != nil || first.Issue != "rate-limited" || first.RetryAt != now.Add(5*time.Minute).UnixMilli() {
		t.Fatalf("bad retry: %#v / %v", first, err)
	}
	if next, _ := finder.Run(context.Background(), "owner", testRequest()); next.RetryAt != first.RetryAt || len(*calls) != 1 {
		t.Fatal("cooldown retried provider")
	}
	key = "new-key"
	if result, _ := finder.Run(context.Background(), "owner", testRequest()); result.State != "ready" || len(*calls) != 3 || (*calls)[1].Header.Get("x-api-key") != key {
		t.Fatal("old credential cooldown blocked replacement")
	}
	key = "old-key"
	if next, _ := finder.Run(context.Background(), "owner", testRequest()); next.RetryAt != first.RetryAt || len(*calls) != 3 {
		t.Fatal("credential cooldown was lost")
	}
	for _, value := range []string{"0", "-1", "nonsense", "99999999999999999999999999999"} {
		wait := retryAfter(now, value).Sub(now)
		if wait < time.Second || wait > 5*time.Minute {
			t.Fatalf("unbounded retry-after %q: %v", value, wait)
		}
	}
}

func TestFinderDoesNotFollowRedirectOrUseAmbientCookies(t *testing.T) {
	calls, hostRedirects := 0, 0
	body := &trackedBody{Reader: strings.NewReader("")}
	client := &http.Client{Jar: fixtureJar{}, CheckRedirect: func(*http.Request, []*http.Request) error { hostRedirects++; return nil },
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.URL.Host != "api.semanticscholar.org" || request.Header.Get("Cookie") != "" {
				t.Fatal("provider boundary or ambient cookie violated")
			}
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"http://127.0.0.1/private"}}, Body: body}, nil
		})}
	finder := New(Options{Catalog: catalogFunc(func(context.Context, string, string) (Reference, error) { return testReference(), nil }),
		ClientForURL: func(context.Context, string) (*http.Client, error) { return client, nil }})
	result, err := finder.Run(context.Background(), "owner", testRequest())
	if err != nil || result.Issue != "unavailable" || calls != 1 || hostRedirects != 0 || !body.closed || client.Jar == nil {
		t.Fatalf("redirect isolation failed: %#v / %v", result, err)
	}
}

func TestFinderCancellationAndBoundedPacing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		cancel()
		return nil, request.Context().Err()
	})}
	finder := New(Options{Catalog: catalogFunc(func(context.Context, string, string) (Reference, error) { return testReference(), nil }),
		ClientForURL: func(context.Context, string) (*http.Client, error) { return client, nil }})
	if _, err := finder.Run(ctx, "owner", testRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation hidden: %v", err)
	}
	finder, calls := testFinder(t, testReference())
	now := time.Unix(1000000, 0)
	finder.now = func() time.Time { return now }
	finder.nextRequest = now.Add(11 * time.Second)
	if result, err := finder.Run(context.Background(), "owner", testRequest()); err != nil || result.Issue != "rate-limited" || len(*calls) != 0 || result.RetryAt != finder.nextRequest.UnixMilli() {
		t.Fatal("unbounded pacing queue")
	}
}
