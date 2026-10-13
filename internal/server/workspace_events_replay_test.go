package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

// A channel-controlled first Flush places arrivals after the first durable
// query. Readiness follows the requested event's flush, not a flush count or
// a sleep, so either per-page or combined flushing is valid.
type workspaceReplayWriter struct {
	mu           sync.Mutex
	header       http.Header
	body         bytes.Buffer
	flushed      int
	firstFlush   chan struct{}
	releaseFirst chan struct{}
	progress     chan struct{}
	firstOnce    sync.Once
	releaseOnce  sync.Once
	failAfter    int
	writes       int
	onWrite      func()
}

func newWorkspaceReplayWriter() *workspaceReplayWriter {
	return &workspaceReplayWriter{
		header: make(http.Header), firstFlush: make(chan struct{}),
		releaseFirst: make(chan struct{}), progress: make(chan struct{}, 1),
	}
}

func (w *workspaceReplayWriter) Header() http.Header { return w.header }
func (w *workspaceReplayWriter) WriteHeader(int)     {}
func (w *workspaceReplayWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.failAfter > 0 && w.writes >= w.failAfter {
		w.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	w.writes++
	n, err := w.body.Write(p)
	w.mu.Unlock()
	if w.onWrite != nil {
		w.onWrite()
	}
	return n, err
}
func (w *workspaceReplayWriter) Flush() {
	w.mu.Lock()
	w.flushed = w.body.Len()
	w.mu.Unlock()
	select {
	case w.progress <- struct{}{}:
	default:
	}
	w.firstOnce.Do(func() {
		close(w.firstFlush)
		<-w.releaseFirst
	})
}
func (w *workspaceReplayWriter) release() {
	w.releaseOnce.Do(func() { close(w.releaseFirst) })
}
func (w *workspaceReplayWriter) snapshot(flushedOnly bool) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	body := w.body.String()
	if flushedOnly {
		return body[:w.flushed]
	}
	return body
}

func workspaceReplayIDs(t *testing.T, body string) []int64 {
	t.Helper()
	ids := make([]int64, 0)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "id: ") {
			id, err := strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil {
				t.Fatalf("invalid SSE id %q: %v", line, err)
			}
			ids = append(ids, id)
		}
	}
	return ids
}

func workspaceReplayWait(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func workspaceReplayWaitID(t *testing.T, w *workspaceReplayWriter, id int64) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		ids := workspaceReplayIDs(t, w.snapshot(true))
		if len(ids) > 0 && ids[len(ids)-1] >= id {
			return
		}
		select {
		case <-w.progress:
		case <-deadline.C:
			t.Fatalf("timed out waiting for flushed id %d; received %v", id, ids)
		}
	}
}

func workspaceReplayFixture(t *testing.T, history int) (*Server, func() workspace.FrameEvent) {
	t.Helper()
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "replay-project", UserID: "local", Name: "Replay"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{ID: "replay-frame", ProjectID: "replay-project", AgentName: "planner", Status: "running", ConversationType: "task"}); err != nil {
		t.Fatal(err)
	}
	ordinal := 0
	appendEvent := func() workspace.FrameEvent {
		t.Helper()
		ordinal++
		event, err := store.AppendFrameEvent(workspace.FrameEventInput{
			ID: fmt.Sprintf("replay-event-%d", ordinal), FrameID: "replay-frame", Type: "frame_update",
			Payload: map[string]any{"ordinal": ordinal},
		})
		if err != nil {
			t.Fatalf("append event %d: %v", ordinal, err)
		}
		return event
	}
	for i := 0; i < history; i++ {
		appendEvent()
	}
	return &Server{workspaceStore: store, workspaceEvents: newWorkspaceEventHub()}, appendEvent
}

func workspaceReplayStart(t *testing.T, app *Server, query, lastID string, w *workspaceReplayWriter) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := newLoopbackTestRequest(http.MethodGet, "/api/go/events/stream?frame_id=replay-frame"+query, nil).WithContext(ctx)
	r.Header.Set("X-Science-User-Id", "local")
	if lastID != "" {
		r.Header.Set("Last-Event-ID", lastID)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.handleWorkspaceEventStream(w, r)
	}()
	t.Cleanup(func() {
		cancel()
		w.release()
		workspaceReplayWait(t, done, "stream cleanup")
	})
	workspaceReplayWait(t, w.firstFlush, "initial replay flush")
	return cancel, done
}

func workspaceReplayStop(t *testing.T, w *workspaceReplayWriter, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	cancel()
	w.release()
	workspaceReplayWait(t, done, "stream cancellation")
}

func workspaceReplayAssertRange(t *testing.T, w *workspaceReplayWriter, first, last int64) {
	t.Helper()
	got := workspaceReplayIDs(t, w.snapshot(false))
	want := make([]int64, 0)
	for id := first; id <= last; id++ {
		want = append(want, id)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replay count=%d want=%d; tail=%v", len(got), len(want), got[max(0, len(got)-5):])
	}
}

func TestWorkspaceEventSSEReplayPageBoundaries(t *testing.T) {
	for _, history := range []int{0, 1, 1000, 1001, 2001} {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			app, appendEvent := workspaceReplayFixture(t, history)
			w := newWorkspaceReplayWriter()
			cancel, done := workspaceReplayStart(t, app, "", "", w)
			// This causally ordered marker also arrives while catch-up is blocked.
			marker := appendEvent()
			app.workspaceEvents.Publish(marker)
			w.release()
			workspaceReplayWaitID(t, w, marker.Sequence)
			workspaceReplayStop(t, w, cancel, done)
			workspaceReplayAssertRange(t, w, 1, marker.Sequence)
		})
	}
}

func TestWorkspaceEventSSEReplayWithoutLiveNotification(t *testing.T) {
	app, _ := workspaceReplayFixture(t, 2001)
	w := newWorkspaceReplayWriter()
	cancel, done := workspaceReplayStart(t, app, "", "", w)
	w.release()
	workspaceReplayWaitID(t, w, 2001)
	workspaceReplayStop(t, w, cancel, done)
	workspaceReplayAssertRange(t, w, 1, 2001)
}

func TestWorkspaceEventSSEReplayResumeCursors(t *testing.T) {
	for _, test := range []struct {
		name, query, header string
		first               int64
	}{
		{"query", "&after_sequence=1000", "", 1001},
		{"header", "", "1000", 1001},
		{"query_precedes_header", "&after_sequence=1000", "2001", 1001},
		{"empty_query_uses_header", "&after_sequence=", "1000", 1001},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, appendEvent := workspaceReplayFixture(t, 2001)
			w := newWorkspaceReplayWriter()
			cancel, done := workspaceReplayStart(t, app, test.query, test.header, w)
			marker := appendEvent()
			app.workspaceEvents.Publish(marker)
			w.release()
			workspaceReplayWaitID(t, w, marker.Sequence)
			workspaceReplayStop(t, w, cancel, done)
			workspaceReplayAssertRange(t, w, test.first, marker.Sequence)
		})
	}
}

func TestWorkspaceEventSSEReplayArrivalAfterShortPage(t *testing.T) {
	app, appendEvent := workspaceReplayFixture(t, 1)
	w := newWorkspaceReplayWriter()
	cancel, done := workspaceReplayStart(t, app, "", "", w)
	// The short page has already been queried. Only the newest arrival is
	// published; the intervening retained event still must precede its ID.
	appendEvent()
	marker := appendEvent()
	app.workspaceEvents.Publish(marker)
	w.release()
	workspaceReplayWaitID(t, w, marker.Sequence)
	workspaceReplayStop(t, w, cancel, done)
	workspaceReplayAssertRange(t, w, 1, marker.Sequence)
}

func TestWorkspaceEventSSEReplayDuplicateAndReorderedNotifications(t *testing.T) {
	app, appendEvent := workspaceReplayFixture(t, 0)
	w := newWorkspaceReplayWriter()
	cancel, done := workspaceReplayStart(t, app, "", "", w)
	one, two := appendEvent(), appendEvent()
	app.workspaceEvents.Publish(two)
	app.workspaceEvents.Publish(one)
	app.workspaceEvents.Publish(two)
	three := appendEvent()
	app.workspaceEvents.Publish(three)
	w.release()
	workspaceReplayWaitID(t, w, three.Sequence)
	workspaceReplayStop(t, w, cancel, done)
	workspaceReplayAssertRange(t, w, 1, three.Sequence)
}

func TestWorkspaceEventSSEReplayReconnect(t *testing.T) {
	app, appendEvent := workspaceReplayFixture(t, 1001)
	w := newWorkspaceReplayWriter()
	cancel, done := workspaceReplayStart(t, app, "", "", w)
	marker := appendEvent()
	app.workspaceEvents.Publish(marker)
	w.release()
	workspaceReplayWaitID(t, w, marker.Sequence)
	workspaceReplayStop(t, w, cancel, done)
	workspaceReplayAssertRange(t, w, 1, marker.Sequence)
	appendEvent()
	next := newWorkspaceReplayWriter()
	nextCancel, nextDone := workspaceReplayStart(t, app, "", strconv.FormatInt(marker.Sequence, 10), next)
	workspaceReplayStop(t, next, nextCancel, nextDone)
	workspaceReplayAssertRange(t, next, marker.Sequence+1, marker.Sequence+1)
}

func TestWorkspaceEventSSEReplayPreservesCapturedLivePayload(t *testing.T) {
	app, appendEvent := workspaceReplayFixture(t, 0)
	w := newWorkspaceReplayWriter()
	cancel, done := workspaceReplayStart(t, app, "", "", w)
	captured := appendEvent()
	if err := app.workspaceStore.DeleteFrame(captured.FrameID); err != nil {
		t.Fatal(err)
	}
	app.workspaceEvents.Publish(captured)
	w.release()
	workspaceReplayWaitID(t, w, captured.Sequence)
	workspaceReplayStop(t, w, cancel, done)
	workspaceReplayAssertRange(t, w, 1, 1)
	payload, err := json.Marshal(captured)
	if err != nil || !strings.Contains(w.snapshot(false), "data: "+string(payload)+"\n") {
		t.Fatalf("captured SSE payload changed: %v", err)
	}
}

func TestWorkspaceEventSSEReplayCancellationBetweenPages(t *testing.T) {
	app, _ := workspaceReplayFixture(t, 2001)
	w := newWorkspaceReplayWriter()
	cancel, done := workspaceReplayStart(t, app, "", "", w)
	flushed := workspaceReplayIDs(t, w.snapshot(true))
	workspaceReplayStop(t, w, cancel, done)
	// Cancellation must not let another page or a queued notification advance
	// beyond the data already flushed at this barrier.
	if got, want := workspaceReplayIDs(t, w.snapshot(false)), flushed; !reflect.DeepEqual(got, want) {
		t.Fatalf("events written after cancellation: got %d, flushed %d", len(got), len(want))
	}
}

func TestWorkspaceEventSSEReplayCancellationDuringPage(t *testing.T) {
	app, _ := workspaceReplayFixture(t, 2001)
	w := newWorkspaceReplayWriter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := 0
	w.onWrite = func() {
		writes++
		if writes == 1005 {
			cancel()
		}
	}
	w.release()
	r := newLoopbackTestRequest(http.MethodGet, "/api/go/events/stream?frame_id=replay-frame", nil).WithContext(ctx)
	r.Header.Set("X-Science-User-Id", "local")
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.handleWorkspaceEventStream(w, r)
	}()
	workspaceReplayWait(t, done, "cancellation during page")
	workspaceReplayAssertRange(t, w, 1, 1005)
}

func TestWorkspaceEventSSEReplayHubOverflowDuringCatchUp(t *testing.T) {
	app, appendEvent := workspaceReplayFixture(t, 2001)
	w := newWorkspaceReplayWriter()
	_, done := workspaceReplayStart(t, app, "", "", w)
	disconnected := 0
	var marker workspace.FrameEvent
	for i := 0; i <= workspaceEventSubscriberBuffer; i++ {
		marker = appendEvent()
		disconnected += app.workspaceEvents.Publish(marker)
	}
	if disconnected != 1 {
		t.Fatalf("disconnected subscribers=%d", disconnected)
	}
	w.release()
	workspaceReplayWait(t, done, "closed bounded hub")
	workspaceReplayAssertRange(t, w, 1, marker.Sequence)
}

func TestWorkspaceEventSSEReplayWriteFailure(t *testing.T) {
	app, _ := workspaceReplayFixture(t, 2001)
	w := newWorkspaceReplayWriter()
	w.failAfter = 1005
	_, done := workspaceReplayStart(t, app, "", "", w)
	w.release()
	workspaceReplayWait(t, done, "write failure")
	workspaceReplayAssertRange(t, w, 1, 1005)
}

func TestWorkspaceEventSSEReplayQueryFailure(t *testing.T) {
	for _, history := range []int{0, 1001} {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			app, _ := workspaceReplayFixture(t, history)
			w := newWorkspaceReplayWriter()
			_, done := workspaceReplayStart(t, app, "", "", w)
			if err := app.workspaceStore.Close(); err != nil {
				t.Fatal(err)
			}
			app.workspaceEvents.Publish(workspace.FrameEvent{FrameID: "replay-frame", Sequence: int64(history + 1), Type: "frame_update"})
			w.release()
			workspaceReplayWait(t, done, "query failure")
			workspaceReplayAssertRange(t, w, 1, int64(min(history, 1000)))
		})
	}
}
