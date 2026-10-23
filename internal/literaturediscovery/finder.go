package literaturediscovery

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Finder owns only bounded, expiring resolution hints. Authorization and the
// Library's durable state remain with the host Catalog on every request.
type Finder struct {
	options             Options
	mu                  sync.Mutex
	resolutions         map[string]resolution
	cooldowns           map[[32]byte]time.Time
	cooldownSalt        [32]byte
	nextRequest         time.Time
	now                 func() time.Time
	requestInterval     time.Duration
	initializationError error
}

func New(options Options) *Finder {
	finder := &Finder{
		options: options, resolutions: make(map[string]resolution), cooldowns: make(map[[32]byte]time.Time),
		now: time.Now, requestInterval: time.Second,
	}
	_, finder.initializationError = rand.Read(finder.cooldownSalt[:])
	return finder
}

func (f *Finder) Run(ctx context.Context, owner string, request Request) (Result, error) {
	if strings.TrimSpace(owner) == "" || len(owner) > 1024 || request.ItemID == "" || request.ItemID != strings.TrimSpace(request.ItemID) || len(request.ItemID) > 512 ||
		request.ExpectedMetadataRevision <= 0 || request.ExpectedMetadataRevision > 9007199254740991 ||
		(request.Direction != "references" && request.Direction != "citations") ||
		request.Offset < 0 || int64(request.Offset) > 9007199254740991 ||
		(request.ResolutionToken != "" && !resolutionTokenValue.MatchString(request.ResolutionToken)) {
		return Result{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result := Result{Schema: "x-science.literature-related.v1", ItemID: request.ItemID,
		MetadataRevision: request.ExpectedMetadataRevision, Direction: request.Direction, State: "ready", Records: []Record{}}
	callContext, cancel := context.WithTimeout(ctx, providerCallLimit)
	defer cancel()
	fail := func(err error) (Result, error) {
		if cancellation := ctx.Err(); cancellation != nil {
			return Result{}, cancellation
		}
		failed := Result{Schema: result.Schema, ItemID: result.ItemID, MetadataRevision: result.MetadataRevision,
			Direction: result.Direction, State: "unavailable", Records: []Record{}, Issue: "unavailable"}
		var source *sourceError
		if errors.As(err, &source) {
			failed.Issue, failed.RetryAt = source.issue, source.retryAt
		} else if errors.Is(err, context.DeadlineExceeded) || callContext.Err() != nil {
			failed.Issue = "timeout"
		}
		return failed, nil
	}
	if f.initializationError != nil {
		return fail(sourceFailure("unavailable"))
	}
	reference, err := f.checkReference(callContext, owner, request)
	if err != nil {
		return fail(err)
	}
	resolved, found := f.resolutionFor(owner, request)
	if request.ResolutionToken != "" && !found {
		return fail(sourceFailure("expired"))
	}
	if !found {
		if request.Offset != 0 {
			return fail(sourceFailure("expired"))
		}
		seed, needsConfirmation, lookupErr := f.resolve(callContext, owner, reference)
		if lookupErr != nil {
			return fail(lookupErr)
		}
		resolved = resolution{owner: owner, itemID: reference.ID, revision: reference.Revision,
			direction: request.Direction, seed: seed}
		if needsConfirmation {
			result.State = "match-required"
		}
	}
	if result.State == "ready" {
		page, pageErr := f.relatedPage(callContext, owner, resolved.seed.PaperID, request)
		if pageErr != nil {
			return fail(pageErr)
		}
		result.Records, result.NextOffset, result.Omitted = page.records, page.next, page.omitted
	}
	// Never return or cache private seed/page data after deletion, merging,
	// ownership loss, or metadata replacement during a provider request.
	if _, err = f.checkReference(callContext, owner, request); err != nil {
		return fail(err)
	}
	if err := callContext.Err(); err != nil {
		return fail(err)
	}
	if !found {
		token, tokenErr := newResolutionToken()
		if tokenErr != nil {
			return fail(sourceFailure("unavailable"))
		}
		resolved.expires = f.now().Add(resolutionLifetime)
		f.rememberResolution(token, resolved)
		result.ResolutionToken = token
	} else {
		result.ResolutionToken = request.ResolutionToken
	}
	seed := cloneRecord(resolved.seed)
	result.Seed = &seed
	return result, nil
}

func (f *Finder) checkReference(ctx context.Context, owner string, request Request) (Reference, error) {
	if err := ctx.Err(); err != nil {
		return Reference{}, err
	}
	if f.options.Catalog == nil {
		return Reference{}, sourceFailure("unavailable")
	}
	reference, err := f.options.Catalog.GetReference(ctx, owner, request.ItemID)
	if ctx.Err() != nil {
		return Reference{}, ctx.Err()
	}
	if err != nil && !errors.Is(err, ErrReferenceUnavailable) {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return Reference{}, err
		}
		return Reference{}, sourceFailure("unavailable")
	}
	if err != nil || reference.ID != request.ItemID || reference.Revision != request.ExpectedMetadataRevision {
		f.invalidateReference(owner, request.ItemID)
		return Reference{}, sourceFailure("reference-changed")
	}
	return reference, nil
}

func (f *Finder) resolve(ctx context.Context, owner string, reference Reference) (Record, bool, error) {
	if identifier := exactProviderID(reference); identifier != "" {
		raw, err := f.read(ctx, owner, "paper/"+url.QueryEscape(identifier), url.Values{"fields": {providerFields}})
		if err != nil {
			return Record{}, false, err
		}
		seed, err := parseRecord(raw)
		return seed, false, err
	}
	title := []rune(strings.TrimSpace(reference.Title))
	if len(title) == 0 {
		return Record{}, false, sourceFailure("no-match")
	}
	if len(title) > 1000 {
		title = title[:1000]
	}
	raw, err := f.read(ctx, owner, "paper/search/match", url.Values{"query": {string(title)}, "fields": {providerFields}})
	if err != nil {
		return Record{}, false, err
	}
	var response struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Data == nil || len(response.Data) > 1 {
		return Record{}, false, sourceFailure("response-invalid")
	}
	if len(response.Data) == 0 {
		return Record{}, false, sourceFailure("no-match")
	}
	seed, err := parseRecord(response.Data[0])
	return seed, true, err
}

type relatedPage struct {
	records []Record
	next    *int
	omitted int
}

func (f *Finder) relatedPage(ctx context.Context, owner, paperID string, request Request) (relatedPage, error) {
	page := relatedPage{records: []Record{}}
	raw, err := f.read(ctx, owner, "paper/"+paperID+"/"+request.Direction,
		url.Values{"fields": {providerFields}, "limit": {"20"}, "offset": {strconv.Itoa(request.Offset)}})
	if err != nil {
		return page, err
	}
	var response struct {
		Offset *int              `json:"offset"`
		Next   *int              `json:"next"`
		Data   []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Offset == nil || *response.Offset != request.Offset ||
		response.Data == nil || len(response.Data) > 20 ||
		(response.Next != nil && (*response.Next <= request.Offset || int64(*response.Next) > 9007199254740991)) {
		return page, sourceFailure("response-invalid")
	}
	seen := make(map[string]struct{})
	field := "citedPaper"
	if request.Direction == "citations" {
		field = "citingPaper"
	}
	for _, rawRow := range response.Data {
		var row map[string]json.RawMessage
		if json.Unmarshal(rawRow, &row) != nil || row == nil {
			return page, sourceFailure("response-invalid")
		}
		nested := bytes.TrimSpace(row[field])
		if len(nested) == 0 || bytes.Equal(nested, []byte("null")) {
			page.omitted++
			continue
		}
		var identity struct {
			PaperID *string `json:"paperId"`
		}
		if json.Unmarshal(nested, &identity) != nil {
			return page, sourceFailure("response-invalid")
		}
		if identity.PaperID == nil || *identity.PaperID == "" {
			page.omitted++
			continue
		}
		entry, recordErr := parseRecord(nested)
		if recordErr != nil {
			return page, recordErr
		}
		if _, duplicate := seen[entry.PaperID]; !duplicate {
			page.records = append(page.records, entry)
			seen[entry.PaperID] = struct{}{}
		}
	}
	page.next = response.Next
	return page, nil
}
