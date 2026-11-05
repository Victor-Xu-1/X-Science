package server

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebReadDiagnosticsReportCookieCatchupWithoutChangingRepair(t *testing.T) {
	logs := captureTranscriptWebReadDiagnostics(t)
	fixture, cookie := newCookieTranscriptWebReadDiagnosticFixture(t)
	base := "/api/conversations/" + transcriptWebServingFrameID + "/messages"
	path := base + "?limit=80&content_mode=compact&probe=private-diagnostic-sentinel"
	request := func(ctx context.Context, target string) *http.Request {
		r := newLoopbackTestRequest(http.MethodGet, target, nil).WithContext(ctx)
		r.RemoteAddr = "127.0.0.1:12345"
		r.AddCookie(cookie)
		r.Header.Set("X-Request-ID", "private-diagnostic-sentinel")
		return r
	}
	// Ready page and detail reads remain unchanged, quiet and writer-independent.
	releaseWriter := holdTranscriptWebUnrelatedWriter(t, fixture)
	for _, target := range []string{path, base + "/" + transcriptWebServingMessageID(0)} {
		response, done := startTranscriptWebDiagnosticRequest(fixture.server, request(t.Context(), target))
		awaitTranscriptWebDiagnosticRequest(t, done, releaseWriter)
		if response.Code != http.StatusOK {
			t.Fatalf("ready cookie history HTTP%d", response.Code)
		}
	}
	releaseWriter()
	assertNoTranscriptWebReadDiagnostic(t, logs)
	if _, _, created, err := fixture.repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
		ClientMessageID: "cookie-stale-client", FrameEventID: "cookie-stale-event",
		MessageUUID: "cookie-stale-message", Text: "private-diagnostic-sentinel",
	}); err != nil || !created {
		t.Fatalf("append stale event: created=%t err=%v", created, err)
	}
	fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	if transcriptWebFenceReady(fence) || !transcriptWebFenceCanCatchUp(fence) {
		t.Fatal("fixture is not an actual catch-up-eligible stale fence")
	}
	releaseWriter = holdTranscriptWebUnrelatedWriter(t, fixture)
	defer releaseWriter()
	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	defer cancelLeader()
	_, leaderDone := startTranscriptWebDiagnosticRequest(fixture.server, request(leaderCtx, path))
	awaitTranscriptWebDiagnosticStack(t, leaderDone, releaseWriter, true)
	followerCtx, cancelFollower := context.WithCancel(t.Context())
	defer cancelFollower()
	_, followerDone := startTranscriptWebDiagnosticRequest(fixture.server, request(followerCtx, path))
	awaitTranscriptWebDiagnosticStack(t, followerDone, releaseWriter, false)
	cancelFollower()
	follower := requireTranscriptWebReadDiagnostic(t, logs, "stage=catchup_follower_wait", "catchup_role=follower", "projection=none")
	awaitTranscriptWebDiagnosticRequest(t, followerDone, releaseWriter)
	cancelLeader()
	leader := requireTranscriptWebReadDiagnostic(t, logs, "stage=project_commit", "catchup_role=leader", "projection=incremental")
	if transcriptWebDiagnosticID(leader) == transcriptWebDiagnosticID(follower) {
		t.Fatal("different requests reused a diagnostic identity")
	}
	// The diagnostic never cancels, releases or reassigns the existing repair.
	select {
	case <-leaderDone:
		t.Fatal("telemetry changed the detached repair's writer wait")
	default:
	}
	releaseWriter()
	awaitTranscriptWebDiagnosticRequest(t, leaderDone, releaseWriter)
	assertNoTranscriptWebReadDiagnostic(t, logs)
}

func TestTranscriptWebReadDiagnosticsObserveCanceledCookieAuthentication(t *testing.T) {
	logs := captureTranscriptWebReadDiagnostics(t)
	fixture, cookie := newCookieTranscriptWebReadDiagnosticFixture(t)
	fixture.server.webSessions.mu.Lock()
	var once sync.Once
	release := func() { once.Do(fixture.server.webSessions.mu.Unlock) }
	defer release()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := newLoopbackTestRequest(http.MethodGet, "/api/conversations/private-diagnostic-sentinel/messages?private-diagnostic-sentinel", nil).WithContext(ctx)
	r.RemoteAddr = "127.0.0.1:12345"
	r.AddCookie(cookie)
	r.Header.Set("X-Request-ID", "private-diagnostic-sentinel")
	_, done := startTranscriptWebDiagnosticRequest(fixture.server, r)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	stack := make([]byte, 256<<10)
	for {
		raw := string(stack[:runtime.Stack(stack, true)])
		if strings.Contains(raw, "(*webSessionStore).Authenticate") && strings.Contains(raw, "withWebSessionMigration.func") {
			break
		}
		select {
		case <-deadline.C:
			release()
			awaitTranscriptWebDiagnosticRequest(t, done, release)
			t.Fatal("cookie request did not reach the real session lock")
		default:
			runtime.Gosched()
		}
	}
	cancel()
	requireTranscriptWebReadDiagnostic(t, logs, "stage=authentication", "catchup_role=none", "projection=none")
	release()
	awaitTranscriptWebDiagnosticRequest(t, done, release)
	assertNoTranscriptWebReadDiagnostic(t, logs)
}

func TestTranscriptWebReadDiagnosticRouteIsBounded(t *testing.T) {
	for _, test := range []struct{ method, path, want string }{
		{http.MethodGet, "/api/conversations/a/messages?secret=x", "history_page"},
		{http.MethodGet, "/api/conversations/a/messages/b", "history_detail"},
		{http.MethodGet, "/api/conversations/a/messages/b/tool-detail?path=secret", "history_detail"},
		{http.MethodGet, "/api/conversations/a/messages/b/unknown", ""},
		{http.MethodGet, "/api/conversations/a/messages/", ""},
		{http.MethodGet, "/api/conversations/a/context-usage", ""},
		{http.MethodPost, "/api/conversations/a/messages", ""},
		{http.MethodGet, "/api/conversations//messages", ""},
	} {
		if got := transcriptWebReadDiagnosticRoute(httptest.NewRequest(test.method, test.path, nil)); got != test.want {
			t.Fatalf("diagnostic route=%q want=%q", got, test.want)
		}
	}
}

func TestTranscriptWebReadDiagnosticLateStageCannotReplaceCancellation(t *testing.T) {
	logs := captureTranscriptWebReadDiagnostics(t)
	noteTranscriptWebReadStage(nil, webReadResponse)
	ctx, cancel := context.WithCancel(t.Context())
	started := time.Now()
	diagnostic := &transcriptWebReadDiagnostic{
		ctx: ctx, id: "0123456789abcdef", route: "history_page", started: started,
		stageStarted: started, stage: webReadFull, role: "leader", mode: "full",
	}
	detached := context.WithoutCancel(context.WithValue(ctx, transcriptWebReadDiagnosticKey{}, diagnostic))
	cancel()
	noteTranscriptWebReadStage(detached, webReadResponse)
	requireTranscriptWebReadDiagnostic(t, logs, "stage=project_full", "catchup_role=leader", "projection=full")
	diagnostic.finish()
	assertNoTranscriptWebReadDiagnostic(t, logs)
	// Slow successful requests use the same safe fields, without an actual wait.
	diagnostic = &transcriptWebReadDiagnostic{
		ctx: t.Context(), id: "fedcba9876543210", route: "history_detail", started: time.Now().Add(-2 * time.Second),
		stageStarted: time.Now(), stage: webReadResponse, role: "none", mode: "none",
	}
	diagnostic.finish()
	select {
	case line := <-logs.lines:
		if !strings.Contains(line, "outcome=completed") || !strings.Contains(line, "stage=response") {
			t.Fatalf("unexpected slow-completion diagnostic: %s", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow completion did not emit a bounded diagnostic")
	}
}

func newCookieTranscriptWebReadDiagnosticFixture(t *testing.T) (transcriptWebReadModelServingFixture, *http.Cookie) {
	t.Helper()
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	readModel, err := fixture.store.TranscriptWebReadModel(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fixture.readModel, fixture.server.transcriptWebReadModel = readModel, readModel
	fixture.server.xscienceLinkAuth = newXScienceLinkAuthenticator(XScienceLinkAuthOptions{
		Username: "operator", Password: "test-secret-password", UserID: transcriptWebServingOwnerID, TTL: time.Hour,
	})
	fixture.server.webAuthenticationState = newWebAuthenticationState(Options{FileRoot: t.TempDir()}, fixture.server.xscienceLinkAuth, nil)
	return fixture, loginWebCookie(t, fixture.server.Handler())
}

func startTranscriptWebDiagnosticRequest(server *Server, request *http.Request) (*httptest.ResponseRecorder, <-chan struct{}) {
	response, done := httptest.NewRecorder(), make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(response, request)
		close(done)
	}()
	return response, done
}

func awaitTranscriptWebDiagnosticRequest(t *testing.T, done <-chan struct{}, release func()) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("synthetic HTTP request did not settle within cleanup budget")
	}
}

func awaitTranscriptWebDiagnosticStack(t *testing.T, done <-chan struct{}, release func(), leader bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	stack := make([]byte, 256<<10)
	for {
		for _, goroutine := range strings.Split(string(stack[:runtime.Stack(stack, true)]), "\n\n") {
			if strings.Contains(goroutine, "catchUpTranscriptWebReadModel") &&
				strings.Contains(goroutine, "RunImmediateWithOutcome") == leader {
				return
			}
		}
		select {
		case <-done:
			t.Fatal("request ended before expected catch-up wait")
		case <-deadline.C:
			release()
			t.Fatal("request did not reach expected catch-up wait")
		default:
			runtime.Gosched()
		}
	}
}

type transcriptWebDiagnosticLogs struct{ lines chan string }

func (capture *transcriptWebDiagnosticLogs) Write(value []byte) (int, error) {
	line := string(value)
	if strings.Contains(line, "history_read_request ") {
		select {
		case capture.lines <- line:
		default:
		}
	}
	return len(value), nil
}

func captureTranscriptWebReadDiagnostics(t *testing.T) *transcriptWebDiagnosticLogs {
	t.Helper()
	capture := &transcriptWebDiagnosticLogs{lines: make(chan string, 16)}
	previous := log.Writer()
	log.SetOutput(io.Writer(capture))
	t.Cleanup(func() { log.SetOutput(previous) })
	return capture
}

func requireTranscriptWebReadDiagnostic(t *testing.T, logs *transcriptWebDiagnosticLogs, fields ...string) string {
	t.Helper()
	select {
	case line := <-logs.lines:
		for _, field := range append(fields, "outcome=canceled", "elapsed_ms=", "stage_ms=", "route=history_page") {
			if !strings.Contains(line, field) {
				t.Fatalf("diagnostic lacks %q: %s", field, line)
			}
		}
		for _, secret := range []string{"private-diagnostic-sentinel", transcriptWebServingFrameID, transcriptWebServingStreamUID,
			"X-Request-ID", "Cookie", "/api/", "test-secret-password", "cookie-stale-message"} {
			if strings.Contains(line, secret) {
				t.Fatalf("diagnostic leaked synthetic private value %q", secret)
			}
		}
		if id := transcriptWebDiagnosticID(line); len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
			t.Fatal("diagnostic did not use an anonymous generated request ID")
		}
		t.Log(strings.TrimSpace(line))
		return line
	case <-time.After(2 * time.Second):
		t.Fatal("canceled request did not emit a bounded diagnostic")
		return ""
	}
}

func transcriptWebDiagnosticID(line string) string {
	for _, field := range strings.Fields(line) {
		if strings.HasPrefix(field, "request_id=") {
			return strings.TrimPrefix(field, "request_id=")
		}
	}
	return ""
}

func assertNoTranscriptWebReadDiagnostic(t *testing.T, logs *transcriptWebDiagnosticLogs) {
	t.Helper()
	select {
	case line := <-logs.lines:
		t.Fatalf("unexpected duplicate or fast-read diagnostic: %s", line)
	default:
	}
}
