package literaturediscovery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const providerOrigin = "https://api.semanticscholar.org/graph/v1/"
const providerFields = "title,abstract,authors,year,venue,externalIds,publicationDate,journal,publicationTypes"
const providerBodyLimit = 2 * 1024 * 1024
const providerCallLimit = 30 * time.Second

type sourceError struct {
	issue   string
	retryAt int64
}

func (err *sourceError) Error() string { return err.issue }
func sourceFailure(issue string) error { return &sourceError{issue: issue} }

// A returned source error is deliberately closed: upstream bodies, URLs and
// credentials must never cross the Library response boundary.
func (f *Finder) read(ctx context.Context, owner, path string, query url.Values) (json.RawMessage, error) {
	if f.options.ClientForURL == nil {
		return nil, sourceFailure("unavailable")
	}
	if _, _, err := f.credentials(ctx, owner); err != nil {
		return nil, err
	}
	if err := f.pace(ctx); err != nil {
		return nil, err
	}
	key, identity, err := f.credentials(ctx, owner)
	if err != nil {
		return nil, err
	}
	address := providerOrigin + path + "?" + query.Encode()
	client, err := f.options.ClientForURL(ctx, address)
	if err != nil {
		return nil, classifyTransportError(err)
	}
	if client == nil {
		return nil, sourceFailure("unavailable")
	}
	// Retain the host's transport/proxy policy, but not its ambient cookie jar or
	// redirect behavior. This request is confined to the fixed public provider.
	confined := *client
	confined.Jar = nil
	confined.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("related provider redirect blocked") }
	if confined.Timeout <= 0 || confined.Timeout > providerCallLimit {
		confined.Timeout = providerCallLimit
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, sourceFailure("unavailable")
	}
	request.Header.Set("Accept", "application/json")
	if key != "" {
		request.Header.Set("x-api-key", key)
	}
	response, err := confined.Do(request)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		return nil, classifyTransportError(err)
	}
	if response == nil {
		return nil, sourceFailure("unavailable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, sourceFailure("authentication")
		case http.StatusNotFound:
			return nil, sourceFailure("no-match")
		case http.StatusTooManyRequests:
			until := retryAfter(f.now(), response.Header.Get("Retry-After"))
			f.setCooldown(identity, until)
			return nil, &sourceError{issue: "rate-limited", retryAt: until.UnixMilli()}
		default:
			return nil, sourceFailure("unavailable")
		}
	}
	if response.Body == nil || response.ContentLength > providerBodyLimit {
		return nil, sourceFailure("response-invalid")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, providerBodyLimit+1))
	if err != nil {
		return nil, classifyTransportError(err)
	}
	if len(body) > providerBodyLimit || !json.Valid(body) {
		return nil, sourceFailure("response-invalid")
	}
	return json.RawMessage(body), nil
}

func classifyTransportError(err error) error {
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return sourceFailure("timeout")
	}
	return sourceFailure("unavailable")
}

func (f *Finder) credentials(ctx context.Context, owner string) (string, [32]byte, error) {
	var identity [32]byte
	key := ""
	if f.options.APIKey != nil {
		var err error
		key, err = f.options.APIKey(ctx, owner)
		if err != nil {
			return "", identity, classifyTransportError(err)
		}
	}
	if strings.ContainsAny(key, "\r\n\x00") || len(key) > 16384 {
		return "", identity, sourceFailure("authentication")
	}
	digest := hmac.New(sha256.New, f.cooldownSalt[:])
	_, _ = digest.Write([]byte(key))
	copy(identity[:], digest.Sum(nil))
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	for candidate, until := range f.cooldowns {
		if !until.After(now) {
			delete(f.cooldowns, candidate)
		}
	}
	if until := f.cooldowns[identity]; until.After(now) {
		return "", identity, &sourceError{issue: "rate-limited", retryAt: until.UnixMilli()}
	}
	return key, identity, nil
}

func (f *Finder) setCooldown(identity [32]byte, until time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if previous := f.cooldowns[identity]; previous.After(until) {
		return
	}
	if _, exists := f.cooldowns[identity]; !exists && len(f.cooldowns) >= resolutionLimit {
		var oldestKey [32]byte
		var oldest time.Time
		for key, value := range f.cooldowns {
			if oldest.IsZero() || value.Before(oldest) {
				oldestKey, oldest = key, value
			}
		}
		delete(f.cooldowns, oldestKey)
	}
	f.cooldowns[identity] = until
}

func (f *Finder) pace(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	now := f.now()
	slot := f.nextRequest
	if slot.Before(now) {
		slot = now
	}
	wait := slot.Sub(now)
	if wait > 10*time.Second {
		f.mu.Unlock()
		return &sourceError{issue: "rate-limited", retryAt: slot.UnixMilli()}
	}
	f.nextRequest = slot.Add(f.requestInterval)
	f.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryAfter(now time.Time, value string) time.Time {
	wait := time.Minute
	if seconds, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64); err == nil {
		// Reject the oversized conversion path explicitly; only 0..300 can
		// reach the signed Duration conversion and nanosecond multiplication.
		if seconds > 300 {
			return now.Add(5 * time.Minute)
		}
		wait = time.Duration(seconds) * time.Second
	} else if date, err := http.ParseTime(value); err == nil {
		wait = date.Sub(now)
	}
	if wait < time.Second {
		wait = time.Second
	}
	if wait > 5*time.Minute {
		wait = 5 * time.Minute
	}
	return now.Add(wait)
}
