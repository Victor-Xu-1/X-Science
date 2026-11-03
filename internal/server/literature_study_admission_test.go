package server

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureStudyAdmissionPreflightAndCancellation(t *testing.T) {
	f, source, input := serverStudyFixture(t)
	owner := f.f.stream.OwnerID
	ready, err := f.f.store.PublishLiteratureStudy(t.Context(), owner, f.itemID, input, workspace.LiteratureStudySnapshot{SourceKey: source.Key, Coverage: "abstract", Model: "synthetic-ready", Sections: workspace.LiteratureStudySections{}})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var calls atomic.Int32
	var wg sync.WaitGroup
	infer := func(ctx context.Context, _ string) (workspace.LiteratureStudySections, string, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return workspace.LiteratureStudySections{}, "synthetic-inflight", nil
		case <-ctx.Done():
			return workspace.LiteratureStudySections{}, "", ctx.Err()
		}
	}
	evidence := func(context.Context, workspace.LiteratureStudySource) (literatureStudyEvidence, error) {
		return literatureStudyEvidence{}, errors.New("abstract fixture")
	}
	type outcome struct{ err error }
	done := make(chan outcome, 8)
	cancels := []context.CancelFunc{}
	launch := func() {
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		next := input
		next.OperationID = uuid.NewString()
		next.ExpectedRevision = ready.Revision
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.f.server.generateLiteratureStudy(ctx, owner, f.itemID, next, evidence, infer)
			done <- outcome{err}
		}()
	}
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		releaseOnce.Do(func() { close(release) })
		wg.Wait()
	}()
	receive := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("controlled inference did not reach boundary")
		}
	}
	for i := 0; i < 4; i++ {
		launch()
		receive(entered)
	}
	launch()
	rejected := false
	select {
	case fifth := <-done:
		if fifth.err == nil {
			t.Error("fifth generation accepted")
		}
		response := httptest.NewRecorder()
		writeLiteratureStudyError(response, fifth.err)
		if response.Code != http.StatusTooManyRequests {
			t.Errorf("busy error is not typed nonblocking 429: %d", response.Code)
		}
		rejected = true
	case <-entered:
		t.Error("fifth request entered inference before an owned permit was released")
	case <-time.After(3 * time.Second):
		t.Error("capacity admission blocked instead of rejecting")
	}
	if !rejected {
		return
	}
	// Source/owner/replay prechecks must precede capacity; saved reads stay usable.
	if result, err := f.f.server.generateLiteratureStudy(t.Context(), owner, f.itemID, input, evidence, infer); err != nil || result.Snapshot == nil || calls.Load() != 4 {
		t.Error("busy prevented exact replay", result, err, calls.Load())
	}
	path := "/api/literature/items/" + f.itemID + "/study-snapshot?language=en-US"
	if response := p3JSONRequest(t, f.f.server, http.MethodGet, path, nil, owner); response.Code != 200 {
		t.Error("busy prevented GET", response.Code)
	}
	next := input
	next.OperationID = uuid.NewString()
	next.ExpectedRevision = ready.Revision
	if _, err := f.f.server.generateLiteratureStudy(t.Context(), "foreign-owner", f.itemID, next, evidence, infer); !errors.Is(err, workspace.ErrLiteratureUnavailable) {
		t.Error("busy masked owner precheck", err)
	}
	wrongSource := next
	wrongSource.ExpectedSourceKey += "-obsolete"
	if _, err := f.f.server.generateLiteratureStudy(t.Context(), owner, f.itemID, wrongSource, evidence, infer); !errors.Is(err, workspace.ErrLiteratureStudySourceChanged) {
		t.Error("busy masked source precheck", err)
	}
	cancels[0]()
	select {
	case cancelled := <-done:
		if !errors.Is(cancelled.err, context.Canceled) {
			t.Error("cancel did not finish", cancelled.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not release request")
	}
	launch()
	receive(entered)
	if calls.Load() != 5 {
		t.Error("cancelled permit was not reusable", calls.Load())
	}
}

func TestLiteratureStudyAdmissionZeroValueServerIsolation(t *testing.T) {
	first, second := &Server{}, &Server{}
	releases := []func(){}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < 4; i++ {
		release, err := first.acquireLiteratureStudyPermit(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := first.acquireLiteratureStudyPermit(t.Context()); !errors.Is(err, errLiteratureStudyBusy) {
		t.Fatal("capacity not enforced", err)
	}
	release, err := second.acquireLiteratureStudyPermit(t.Context())
	if err != nil {
		t.Fatal("different Server shared capacity", err)
	}
	release()
	release()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := second.acquireLiteratureStudyPermit(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled admission", err)
	}
	releases[0]()
	releases[0]()
	again, err := first.acquireLiteratureStudyPermit(t.Context())
	if err != nil {
		t.Fatal("idempotent release lost permit", err)
	}
	again()
}
