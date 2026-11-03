package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
	workspace "x-science/internal/persistence/workspace"
)

const studyRuntimeOwner = "study-runtime-owner"

// No chat frame, runner claim, kernel job or other durable work is created.
func studyRuntimeFixture(t *testing.T, wait func()) (*Server, *workspace.Store, workspace.LiteratureStudySource, workspace.LiteratureStudyGenerate) {
	t.Helper()
	store, err := workspace.Open(filepath.Join(t.TempDir(), "study-runtime.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := newV11TestServer(t, Options{Workspace: store, FileRoot: t.TempDir(), TryDrainIdleDurableWait: wait})
	t.Cleanup(func() { closeTestServer(t, app) })
	created, err := store.CreateManualLiterature(t.Context(), studyRuntimeOwner, workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Synthetic non-chat reference", Metadata: map[string]any{"abstract": "A synthetic abstract."}})
	if err != nil {
		t.Fatal(err)
	}
	source, view, err := store.ReadLiteratureStudy(t.Context(), studyRuntimeOwner, created.Item.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if sessions, operations, draining := app.runtimeActivity(); sessions != 0 || operations != 0 || draining || app.activeDurableRunnerAttemptCount() != 0 || app.activeKernelExecutionCount() != 0 || app.activeKernelOperationCount() != 0 || app.hasPendingDelegatedStopProofs() {
		t.Fatal("fixture has unrelated runtime/durable work; not a Study idle-fence reproduction")
	}
	return app, store, source, workspace.LiteratureStudyGenerate{OperationID: uuid.NewString(), Language: "en-US", ExpectedSourceKey: source.Key, ExpectedRevision: view.Revision}
}
func TestLiteratureStudyOwnedRuntimeBlocksIdleUntilReturn(t *testing.T) {
	for _, mode := range []string{"completed", "cancelled", "cancel-ignored-until-return"} {
		t.Run(mode, func(t *testing.T) {
			app, store, source, input := studyRuntimeFixture(t, nil)
			entered, release, cancelObserved := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			open := func() { releaseOnce.Do(func() { close(release) }) }
			ctx, cancel := context.WithCancel(t.Context())
			type outcome struct {
				view workspace.LiteratureStudyView
				err  error
			}
			done := make(chan outcome, 1)
			joined := false
			defer func() {
				cancel()
				open()
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("controlled request failed to join")
					}
				}
			}()
			go func() {
				value, err := app.generateLiteratureStudy(ctx, studyRuntimeOwner, source.ItemID, input,
					func(context.Context, workspace.LiteratureStudySource) (literatureStudyEvidence, error) {
						t.Error("abstract fixture unexpectedly read a PDF")
						return literatureStudyEvidence{}, errors.New("not needed")
					},
					func(ctx context.Context, _ string) (workspace.LiteratureStudySections, string, error) {
						close(entered)
						switch mode {
						case "cancelled":
							<-ctx.Done()
							return workspace.LiteratureStudySections{}, "", ctx.Err()
						case "cancel-ignored-until-return":
							<-ctx.Done()
							close(cancelObserved)
							<-release
						default:
							<-release
						}
						return workspace.LiteratureStudySections{}, "synthetic-inference", nil
					})
				done <- outcome{value, err}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("controlled Study inference did not enter")
			}
			sessions, operations, _ := app.runtimeActivity()
			drained := app.TryDrainIdle()
			t.Logf("inflight non-chat Study: sessions=%d operations=%d TryDrainIdle=%t", sessions, operations, drained)
			if sessions != 0 || operations != 1 || drained {
				t.Errorf("Study was unowned while actual idle fence admitted drain: %d/%d/%t", sessions, operations, drained)
			}
			if operations == 0 && !drained {
				t.Error("missing operation observed but durable fixture prevented the target drain; not qualified target RED")
			}
			if mode != "completed" {
				cancel()
			}
			if mode == "cancel-ignored-until-return" {
				select {
				case <-cancelObserved:
				case <-time.After(5 * time.Second):
					t.Fatal("cancel not observed")
				}
				_, stillOwned, _ := app.runtimeActivity()
				if stillOwned != 1 || app.TryDrainIdle() {
					t.Error("cancel released ownership before real synchronous return")
				}
			}
			if mode != "cancelled" {
				open()
			}
			var result outcome
			select {
			case result = <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("Study did not settle")
			}
			if mode == "completed" {
				if result.err != nil || result.view.Snapshot == nil {
					t.Error("normal publication failed", result.err)
				}
			} else if !errors.Is(result.err, context.Canceled) {
				t.Error("cancel result", result.err)
			}
			_, read, err := store.ReadLiteratureStudy(t.Context(), studyRuntimeOwner, source.ItemID, input.Language)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "completed" && read.Snapshot != nil {
				t.Error("cancel published ready")
			}
			if sessions, operations, _ := app.runtimeActivity(); sessions != 0 || operations != 0 || !app.TryDrainIdle() {
				t.Errorf("real return retained operation: %d/%d", sessions, operations)
			}
		})
	}
}
func TestLiteratureStudyRuntimeDrainAdmissionPreservesReads(t *testing.T) {
	for _, mode := range []string{"draining", "evaluating"} {
		t.Run(mode, func(t *testing.T) {
			evaluating, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			open := func() { once.Do(func() { close(release) }) }
			var wait func()
			if mode == "evaluating" {
				wait = func() { close(evaluating); <-release }
			}
			app, store, source, input := studyRuntimeFixture(t, wait)
			first, err := store.PublishLiteratureStudy(t.Context(), studyRuntimeOwner, source.ItemID, input, workspace.LiteratureStudySnapshot{SourceKey: source.Key, Coverage: "abstract", Model: "synthetic-cached", Sections: workspace.LiteratureStudySections{}})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan bool, 1)
			if mode == "evaluating" {
				go func() { done <- app.TryDrainIdle() }()
				defer func() {
					open()
					select {
					case accepted := <-done:
						if !accepted {
							t.Error("rejected generation blocked drain completion")
						}
					case <-time.After(5 * time.Second):
						t.Error("drain did not join")
					}
				}()
				select {
				case <-evaluating:
				case <-time.After(5 * time.Second):
					t.Fatal("evaluation did not enter")
				}
			} else if !app.TryDrainIdle() {
				t.Fatal("quiescent fixture did not really enter draining")
			}
			called := false
			evidence := func(context.Context, workspace.LiteratureStudySource) (literatureStudyEvidence, error) {
				called = true
				return literatureStudyEvidence{}, errors.New("must not run")
			}
			infer := func(context.Context, string) (workspace.LiteratureStudySections, string, error) {
				called = true
				return workspace.LiteratureStudySections{}, "must-not-run", nil
			}
			next := input
			next.OperationID = uuid.NewString()
			next.ExpectedRevision = first.Revision
			_, err = app.generateLiteratureStudy(t.Context(), studyRuntimeOwner, source.ItemID, next, evidence, infer)
			if !errors.Is(err, ErrRuntimeDraining) || called {
				t.Error("new inference crossed original drain fence", err, called)
			}
			response := httptest.NewRecorder()
			writeLiteratureStudyError(response, err)
			var body map[string]any
			_ = json.Unmarshal(response.Body.Bytes(), &body)
			if response.Code != http.StatusServiceUnavailable || body["code"] != "runtime_draining" {
				t.Error("draining response lost original typed retry contract", response.Code, body)
			}
			cached, err := app.generateLiteratureStudy(t.Context(), studyRuntimeOwner, source.ItemID, input, evidence, infer)
			if err != nil || cached.Revision != first.Revision || cached.Snapshot == nil || called {
				t.Error("draining blocked exact committed replay", cached, err, called)
			}
			path := "/api/literature/items/" + source.ItemID + "/study-snapshot?language=en-US"
			get := p3JSONRequest(t, app, http.MethodGet, path, nil, studyRuntimeOwner)
			if get.Code != http.StatusOK {
				t.Error("draining blocked saved read", get.Code, get.Body.String())
			}
			_, after, readErr := store.ReadLiteratureStudy(t.Context(), studyRuntimeOwner, source.ItemID, input.Language)
			if readErr != nil || after.Revision != first.Revision {
				t.Error("rejected generation published", after, readErr)
			}
			if _, operations, _ := app.runtimeActivity(); operations != 0 {
				t.Error("rejected generation retained runtime operation", operations)
			}
		})
	}
}
