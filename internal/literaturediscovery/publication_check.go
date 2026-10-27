package literaturediscovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

const publicationMaxBytes = 2 * 1024 * 1024

// ClientFor must reuse the application's public-source route and DNS policy.
// It is deliberately not an endpoint option or a browser-supplied URL.
type ClientFor func(context.Context, string) (*http.Client, error)

type PublicationChecker struct {
	clientFor ClientFor
	now       func() time.Time
	mu        sync.Mutex
	retryAt   time.Time
	active    int
}

func NewPublicationChecker(clientFor ClientFor) *PublicationChecker {
	return &PublicationChecker{clientFor: clientFor, now: time.Now}
}

func (checker *PublicationChecker) begin() PublicationIssue {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if checker.now().Before(checker.retryAt) {
		return PublicationRateLimited
	}
	if checker.active >= 4 {
		return PublicationUnavailable
	}
	checker.active++
	return ""
}

func (checker *PublicationChecker) end() {
	checker.mu.Lock()
	checker.active--
	checker.mu.Unlock()
}

func (checker *PublicationChecker) rateLimited(header string) {
	now := checker.now()
	wait := time.Minute
	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil && seconds >= 0 {
		wait = time.Duration(min(seconds, 300)) * time.Second
	} else if date, err := http.ParseTime(header); err == nil {
		wait = date.Sub(now)
	}
	if wait < time.Second {
		wait = time.Second
	}
	if wait > 5*time.Minute {
		wait = 5 * time.Minute
	}
	checker.mu.Lock()
	if until := now.Add(wait); until.After(checker.retryAt) {
		checker.retryAt = until
	}
	checker.mu.Unlock()
}

// Check performs one bounded Crossref read. Store/receipt commits belong to the
// existing Library transaction owner; cancellation never produces a snapshot.
// The reverse updated-by edge matches Open Science9607's publication-check
// semantics. update-to describes other works and must never be used here.
func (checker *PublicationChecker) Check(caller context.Context, doi string) (PublicationSnapshot, PublicationIssue, error) {
	var empty PublicationSnapshot
	canonical, valid := NormalizeDOI(doi)
	if !valid || canonical != doi {
		return empty, PublicationResponseInvalid, nil
	}
	if err := caller.Err(); err != nil {
		return empty, "", err
	}
	if checker.clientFor == nil {
		return empty, PublicationUnavailable, nil
	}
	if issue := checker.begin(); issue != "" {
		return empty, issue, nil
	}
	defer checker.end()
	ctx, cancel := context.WithTimeout(caller, 15*time.Second)
	defer cancel()
	rawURL := "https://api.crossref.org/works/" + url.PathEscape(doi)
	base, err := checker.clientFor(ctx, rawURL)
	if err != nil || base == nil {
		if caller.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return empty, PublicationTimeout, nil
		}
		return empty, PublicationUnavailable, caller.Err()
	}
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return empty, PublicationResponseInvalid, nil
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if caller.Err() != nil {
			return empty, "", caller.Err()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return empty, PublicationTimeout, nil
		}
		return empty, PublicationUnavailable, nil
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		checker.rateLimited(response.Header.Get("Retry-After"))
		return empty, PublicationRateLimited, caller.Err()
	}
	if response.StatusCode == http.StatusNotFound {
		return empty, PublicationNotFound, caller.Err()
	}
	if response.StatusCode != http.StatusOK {
		return empty, PublicationUnavailable, caller.Err()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, publicationMaxBytes+1))
	if caller.Err() != nil {
		return empty, "", caller.Err()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return empty, PublicationTimeout, nil
	}
	if err != nil || len(raw) > publicationMaxBytes || !utf8.Valid(raw) {
		return empty, PublicationResponseInvalid, nil
	}
	notices, valid := publicationNotices(raw, doi)
	if !valid {
		return empty, PublicationResponseInvalid, nil
	}
	if err := ctx.Err(); err != nil {
		if caller.Err() != nil {
			return empty, "", caller.Err()
		}
		return empty, PublicationTimeout, nil
	}
	return PublicationSnapshot{DOI: doi, CheckedAt: checker.now().UTC(), Notices: notices}, "", nil
}
