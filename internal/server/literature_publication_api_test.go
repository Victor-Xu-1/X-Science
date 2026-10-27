package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"x-science/internal/literaturediscovery"
	workspace "x-science/internal/persistence/workspace"
)

type publicationHTTPTransport func(*http.Request) (*http.Response, error)

func (run publicationHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return run(r) }

func TestLiteraturePublicationHTTPUsesOriginalOwnerAndDurableReceipt(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	metadata := map[string]any{"doi": "https://doi.org/10.1234/SOURCE", "large": json.Number("9007199254740993")}
	if _, err := f.f.store.PatchLiteratureItem(t.Context(), owner, f.itemID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var status atomic.Int32
	status.Store(http.StatusOK)
	f.f.server.publicationChecker = literaturediscovery.NewPublicationChecker(func(ctx context.Context, raw string) (*http.Client, error) {
		return &http.Client{Transport: publicationHTTPTransport(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			if raw != "https://api.crossref.org/works/10.1234%2Fsource" || r.Method != http.MethodGet {
				t.Error("wrong provider route")
			}
			return &http.Response{StatusCode: int(status.Load()), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":"ok","message-type":"work","message":{"DOI":"10.1234/source","updated-by":[{"DOI":"10.1234/correction","type":"correction"}]}}`)), Request: r}, nil
		})}, nil
	}, f.f.store)
	path := "/api/literature/items/" + f.itemID + "/publication-check"
	read := p3JSONRequest(t, f.f.server, http.MethodGet, path+"?expected_metadata_revision=2", nil, owner)
	var initial literaturediscovery.PublicationResult
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &initial) != nil || initial.Snapshot != nil || initial.DOI != "10.1234/source" || calls.Load() != 0 {
		t.Fatalf("saved read queried provider or lost source: %d %s", read.Code, read.Body.String())
	}
	op := uuid.NewString()
	body := map[string]any{"operation_id": op, "expected_metadata_revision": 2}
	first := p3JSONRequest(t, f.f.server, http.MethodPost, path, body, owner)
	var receipt literaturediscovery.PublicationReceipt
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &receipt) != nil || receipt.OperationID != op || receipt.Result.Snapshot == nil || len(receipt.Result.Snapshot.Notices) != 1 || calls.Load() != 1 {
		t.Fatalf("check failed: %d %s", first.Code, first.Body.String())
	}
	title := "Edited title"
	if _, err := f.f.store.PatchLiteratureItem(t.Context(), owner, f.itemID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 2, Title: &title}); err != nil {
		t.Fatal(err)
	}
	replay := p3JSONRequest(t, f.f.server, http.MethodPost, path, body, owner)
	if replay.Code != http.StatusOK || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) || calls.Load() != 1 {
		t.Fatal("original request was re-executed after metadata revision changed")
	}
	conflict := p3JSONRequest(t, f.f.server, http.MethodPost, path, map[string]any{"operation_id": op, "expected_metadata_revision": 3}, owner)
	if conflict.Code != http.StatusConflict || calls.Load() != 1 {
		t.Fatal("changed original payload reached provider")
	}
	current := p3JSONRequest(t, f.f.server, http.MethodGet, path+"?expected_metadata_revision=3", nil, owner)
	var saved literaturediscovery.PublicationResult
	if current.Code != http.StatusOK || json.Unmarshal(current.Body.Bytes(), &saved) != nil || saved.Snapshot == nil || !saved.Snapshot.CheckedAt.Equal(receipt.Result.Snapshot.CheckedAt) || saved.MetadataRevision != 3 || calls.Load() != 1 {
		t.Fatal("title edit expired or rewrote saved source evidence")
	}
	status.Store(http.StatusTooManyRequests)
	failed := p3JSONRequest(t, f.f.server, http.MethodPost, path, map[string]any{"operation_id": uuid.NewString(), "expected_metadata_revision": 3}, owner)
	var limited literaturediscovery.PublicationReceipt
	if failed.Code != http.StatusOK || json.Unmarshal(failed.Body.Bytes(), &limited) != nil || limited.Result.Issue != literaturediscovery.PublicationRateLimited || limited.Result.Snapshot == nil || !limited.Result.Snapshot.CheckedAt.Equal(saved.Snapshot.CheckedAt) || calls.Load() != 2 {
		t.Fatal("provider failure did not settle with the original saved evidence")
	}
	item, err := f.f.store.GetLiteratureItem(t.Context(), owner, f.itemID)
	if err != nil || item.MetadataRevision != 3 || item.Metadata["large"] != json.Number("9007199254740993") {
		t.Fatal("publication check changed metadata or its numeric precision")
	}
}

func TestLiteraturePublicationHTTPRejectsUnboundRequestsBeforeProvider(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	path := "/api/literature/items/" + f.itemID + "/publication-check"
	for _, tc := range []struct {
		method, suffix string
		body           any
		owner          string
		status         int
	}{
		{http.MethodGet, "", nil, owner, 400},
		{http.MethodGet, "?expected_metadata_revision=1&expected_metadata_revision=1", nil, owner, 400},
		{http.MethodGet, "?expected_metadata_revision=01", nil, owner, 400},
		{http.MethodGet, "?expected_metadata_revision=9007199254740992", nil, owner, 400},
		{http.MethodGet, "?expected_metadata_revision=2", nil, owner, 409},
		{http.MethodGet, "?expected_metadata_revision=1", nil, uuid.NewString(), 404},
		{http.MethodPost, "", map[string]any{"operation_id": uuid.NewString(), "expected_metadata_revision": 1, "doi": "10.1234/other"}, owner, 400},
		{http.MethodPost, "?url=https://example.org", map[string]any{"operation_id": uuid.NewString(), "expected_metadata_revision": 1}, owner, 400},
		{http.MethodPost, "", map[string]any{"operation_id": "invalid", "expected_metadata_revision": 1}, owner, 400},
		{http.MethodDelete, "", nil, owner, 405},
	} {
		got := p3JSONRequest(t, f.f.server, tc.method, path+tc.suffix, tc.body, tc.owner)
		if got.Code != tc.status {
			t.Fatalf("%s %s: HTTP%d want%d %s", tc.method, tc.suffix, got.Code, tc.status, got.Body.String())
		}
	}
	// No DOI is a definite result rather than an uncertain forever-pending run.
	missing := p3JSONRequest(t, f.f.server, http.MethodPost, path, map[string]any{"operation_id": uuid.NewString(), "expected_metadata_revision": 1}, owner)
	var receipt literaturediscovery.PublicationReceipt
	if missing.Code != 200 || json.Unmarshal(missing.Body.Bytes(), &receipt) != nil || receipt.Result.Issue != literaturediscovery.PublicationDOIRequired || receipt.Result.Snapshot != nil {
		t.Fatalf("missing DOI did not settle: %d %s", missing.Code, missing.Body.String())
	}
}
