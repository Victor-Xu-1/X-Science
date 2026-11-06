package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebCatchupFlightDiagnosticsCorrelateOwnersAndCanceledFollower(t *testing.T) {
	for _, owner := range []string{"request", "background"} {
		t.Run(owner, func(t *testing.T) {
			logs := captureTranscriptWebCatchupDiagnostics(t)
			fixture := newTranscriptWebSharedFullRebuildFixture(t)
			fixture.server.xscienceLinkAuth = newXScienceLinkAuthenticator(XScienceLinkAuthOptions{
				Username: "operator", Password: "test-secret-password", UserID: transcriptWebServingOwnerID, TTL: time.Hour,
			})
			fixture.server.webAuthenticationState = newWebAuthenticationState(Options{FileRoot: t.TempDir()}, fixture.server.xscienceLinkAuth, nil)
			cookie := loginWebCookie(t, fixture.server.Handler())
			logs.clear()
			request := func(ctx context.Context) *http.Request {
				r := newLoopbackTestRequest(http.MethodGet,
					"/api/conversations/"+transcriptWebServingFrameID+"/messages?private=diagnostic-sentinel", nil).WithContext(ctx)
				r.RemoteAddr = "127.0.0.1:12345"
				r.AddCookie(cookie)
				r.Header.Set("X-Request-ID", "private-diagnostic-sentinel")
				return r
			}
			release := holdTranscriptWebUnrelatedWriter(t, fixture)
			defer release()
			leaderCtx, cancelLeader := context.WithCancel(t.Context())
			defer cancelLeader()
			leaderDone := make(chan error, 1)
			if owner == "background" {
				go func() { leaderDone <- fixture.server.runTranscriptWebReadModelBatch(leaderCtx, 1) }()
			} else {
				_, done := startTranscriptWebDiagnosticRequest(fixture.server, request(leaderCtx))
				go func() { <-done; leaderDone <- nil }()
			}
			awaitTranscriptWebSharedRebuildStack(t, release, false)
			followerCtx, cancelFollower := context.WithCancel(t.Context())
			defer cancelFollower()
			_, followerDone := startTranscriptWebDiagnosticRequest(fixture.server, request(followerCtx))
			awaitTranscriptWebSharedRebuildStack(t, release, true)
			cancelFollower()
			awaitTranscriptWebDiagnosticRequest(t, followerDone, release)
			select {
			case err := <-leaderDone:
				t.Fatalf("follower cancellation released the existing %s owner: %v", owner, err)
			default:
			}
			if owner == "request" {
				cancelLeader() // The original detached owner must still finish its work.
			}
			release()
			awaitTranscriptWebSharedRebuildResult(t, leaderDone)
			fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if !transcriptWebFenceReady(fence) || fence.StateMessageCount != 2 {
				t.Fatalf("diagnostics changed the real shared rebuild: %#v", fence)
			}

			lines := logs.snapshot()
			var summaries, waits, requests []string
			for _, line := range lines {
				for _, secret := range []string{"private-diagnostic-sentinel", "diagnostic-sentinel", transcriptWebServingFrameID,
					transcriptWebServingStreamUID, "synthetic.txt", "synthetic tool result", "test-secret-password", "Cookie", "/api/"} {
					if strings.Contains(line, secret) {
						t.Fatalf("catch-up diagnostic leaked %q: %s", secret, line)
					}
				}
				switch {
				case strings.Contains(line, "history_catchup_flight "):
					summaries = append(summaries, line)
				case strings.Contains(line, "history_catchup_wait "):
					waits = append(waits, line)
				case strings.Contains(line, "history_read_request "):
					requests = append(requests, line)
				}
			}
			if len(summaries) != 1 || len(waits) != 1 {
				t.Fatalf("one real flight/follower must have one summary and correlation: summaries=%d waits=%d lines=%q",
					len(summaries), len(waits), lines)
			}
			summary, wait := catchupDiagnosticFields(summaries[0]), catchupDiagnosticFields(waits[0])
			id := summary["flight_id"]
			if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" || wait["flight_id"] != id ||
				summary["owner"] != owner || wait["owner"] != owner || summary["stage"] != "project_commit" ||
				summary["projection"] != "full" || summary["outcome"] != "ok" || wait["outcome"] != "canceled" {
				t.Fatalf("flight identity or completion changed: summary=%q wait=%q", summaries[0], waits[0])
			}
			for _, field := range []struct{ value, name string }{
				{summary["elapsed_ms"], "elapsed_ms"}, {summary["stage_ms"], "stage_ms"}, {wait["wait_ms"], "wait_ms"},
			} {
				if value, err := strconv.ParseFloat(field.value, 64); err != nil || value < 0 {
					t.Fatalf("invalid %s=%q", field.name, field.value)
				}
			}
			matched := false
			oldContract := regexp.MustCompile(`history_read_request request_id=[0-9a-f]{16} route=history_page stage=[a-z_]+ outcome=canceled elapsed_ms=\d+\.\d{3} stage_ms=\d+\.\d{3} catchup_role=(?:leader|follower) projection=(?:none|incremental|full)\n$`)
			for _, line := range requests {
				if !oldContract.MatchString(line) {
					t.Fatalf("immutable request diagnostic contract changed: %q", line)
				}
				fields := catchupDiagnosticFields(line)
				if fields["catchup_role"] == "follower" && fields["request_id"] == wait["request_id"] {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("canceled follower lost its original request identity: wait=%q requests=%q", waits[0], requests)
			}
			t.Log(strings.TrimSpace(summaries[0]))
			t.Log(strings.TrimSpace(waits[0]))
		})
	}
}

func TestTranscriptWebCatchupDiagnosticsOutliveCanceledRequestStage(t *testing.T) {
	logs := captureTranscriptWebCatchupDiagnostics(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	now := time.Now()
	request := &transcriptWebReadDiagnostic{
		ctx: ctx, id: "0123456789abcdef", route: "history_page", started: now, stageStarted: now,
		stage: webReadFull, role: "leader", mode: "full",
	}
	flight := newTranscriptWebCatchupDiagnostic(transcriptWebCatchupRequest)
	detached := withTranscriptWebCatchupDiagnostic(context.WithoutCancel(
		context.WithValue(ctx, transcriptWebReadDiagnosticKey{}, request)), flight)
	noteTranscriptWebReadStage(detached, webReadFull)
	cancel()
	noteTranscriptWebReadStage(detached, webReadMetadata)
	flight.finish(nil)
	flight.finish(errors.New("private-error-sentinel"))
	lines := logs.snapshot()
	if len(lines) != 2 {
		t.Fatalf("one frozen request and one completed owner expected: %q", lines)
	}
	old, final := catchupDiagnosticFields(lines[0]), catchupDiagnosticFields(lines[1])
	if old["stage"] != "project_full" || old["outcome"] != "canceled" ||
		final["stage"] != "project_metadata" || final["projection"] != "full" || final["outcome"] != "ok" {
		t.Fatalf("detached owner lost progress or thawed canceled request: %q", lines)
	}
}

func TestTranscriptWebCatchupDiagnosticsUseBoundedReturnClasses(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"ok", nil}, {"canceled", context.Canceled}, {"deadline", context.DeadlineExceeded},
		{"branch_stale", transcriptstore.ErrBranchStateStale},
		{"projection_stale", transcriptstore.ErrTranscriptWebProjectionStale},
		{"failed", errors.New("private-error-sentinel\nprivate-detail")},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := captureTranscriptWebCatchupDiagnostics(t)
			server := &Server{}
			key := transcriptWebCatchupKey{ownerID: "private-owner", streamUID: "private-stream", branchID: "private-branch"}
			flight, leader := server.startTranscriptWebCatchupFlight(key, transcriptWebCatchupBackground)
			if !leader {
				t.Fatal("synthetic owner was not admitted")
			}
			noteTranscriptWebReadStage(withTranscriptWebCatchupDiagnostic(t.Context(), flight.diagnostic), webReadIncremental)
			if test.err != nil {
				test.err = fmt.Errorf("private-error-sentinel: %w", test.err)
			}
			server.finishTranscriptWebCatchupFlight(key, flight, test.err)
			select {
			case <-flight.done:
			default:
				t.Fatal("diagnostic completion did not preserve published owner completion")
			}
			flight.diagnostic.finish(test.err)
			lines := logs.snapshot()
			if len(lines) != 1 || strings.Contains(lines[0], "private-") {
				t.Fatalf("completion duplicated or leaked raw data: %q", lines)
			}
			fields := catchupDiagnosticFields(lines[0])
			if fields["outcome"] != test.name || fields["owner"] != "background" || fields["projection"] != "incremental" {
				t.Fatalf("unsafe outcome or lost owner mode: %q", lines)
			}
		})
	}
}

type transcriptWebCatchupLogs struct {
	mu    sync.Mutex
	lines []string
}

func (capture *transcriptWebCatchupLogs) Write(value []byte) (int, error) {
	line := string(value)
	if strings.Contains(line, "history_read_request ") || strings.Contains(line, "history_catchup_") {
		capture.mu.Lock()
		capture.lines = append(capture.lines, line)
		capture.mu.Unlock()
	}
	return len(value), nil
}

func (capture *transcriptWebCatchupLogs) clear() {
	capture.mu.Lock()
	capture.lines = nil
	capture.mu.Unlock()
}

func (capture *transcriptWebCatchupLogs) snapshot() []string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]string(nil), capture.lines...)
}

func captureTranscriptWebCatchupDiagnostics(t *testing.T) *transcriptWebCatchupLogs {
	t.Helper()
	capture := &transcriptWebCatchupLogs{}
	previous := log.Writer()
	log.SetOutput(io.Writer(capture))
	t.Cleanup(func() { log.SetOutput(previous) })
	return capture
}

func catchupDiagnosticFields(line string) map[string]string {
	fields := make(map[string]string)
	for _, value := range strings.Fields(line) {
		if key, value, found := strings.Cut(value, "="); found {
			fields[key] = value
		}
	}
	return fields
}
