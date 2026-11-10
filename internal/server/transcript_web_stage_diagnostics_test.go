package server

import (
	"context"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTranscriptWebCatchupStageBreakdownPreservesFailureAndStageReentry(t *testing.T) {
	logs := captureTranscriptWebStageDiagnostics(t)
	flight := newTranscriptWebCatchupDiagnostic(transcriptWebCatchupBackground)
	ctx := withTranscriptWebCatchupDiagnostic(t.Context(), flight)
	noteTranscriptWebReadStage(ctx, webReadFull)
	noteTranscriptWebReadStage(ctx, webReadMetadata)
	noteTranscriptWebReadStage(ctx, webReadFull)
	flight.finish(errors.New("private-stage-error /private/research/path SELECT secret"))
	flight.finish(nil)
	noteTranscriptWebReadStage(ctx, webReadCommit)
	lines := logs.snapshot()
	original := requireHistoryStageLine(t, lines, "history_catchup_flight ")
	breakdown := requireHistoryStageLine(t, lines, "history_catchup_stages ")
	if original["flight_id"] != breakdown["flight_id"] || breakdown["owner"] != "background" ||
		breakdown["outcome"] != "failed" || breakdown["final_stage"] != "project_full" || breakdown["projection"] != "full" {
		t.Fatalf("failure stage identity changed: %#v", breakdown)
	}
	for _, field := range []string{"elapsed_ms", "lookup_ms", "incremental_ms", "full_ms", "metadata_ms", "records_ms", "commit_ms"} {
		requireHistoryStageMilliseconds(t, breakdown, field)
	}
	if breakdown["records_ms"] != "0.000" || breakdown["commit_ms"] != "0.000" {
		t.Fatalf("unvisited stages received time: %#v", breakdown)
	}
	assertHistoryStagePrivacy(t, lines)
}

func TestTranscriptWebReadStageBreakdownFreezesCancellationWhileOwnerFinishes(t *testing.T) {
	logs := captureTranscriptWebStageDiagnostics(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	now := time.Now()
	request := &transcriptWebReadDiagnostic{
		ctx: ctx, id: "0123456789abcdef", route: "history_page", started: now, stageStarted: now,
		stage: webReadCatchupWait, role: "follower", mode: "none",
	}
	flight := newTranscriptWebCatchupDiagnostic(transcriptWebCatchupBackground)
	detached := withTranscriptWebCatchupDiagnostic(context.WithoutCancel(
		context.WithValue(ctx, transcriptWebReadDiagnosticKey{}, request)), flight)
	cancel()
	noteTranscriptWebReadStage(detached, webReadFull)
	noteTranscriptWebReadStage(detached, webReadMetadata)
	noteTranscriptWebReadStage(detached, webReadRecords)
	noteTranscriptWebReadStage(detached, webReadCommit)
	request.finish()
	flight.finish(nil)
	flight.finish(context.DeadlineExceeded)
	lines := logs.snapshot()
	original := requireHistoryStageLine(t, lines, "history_read_request ")
	read := requireHistoryStageLine(t, lines, "history_read_stages ")
	owner := requireHistoryStageLine(t, lines, "history_catchup_stages ")
	if read["request_id"] != request.id || original["request_id"] != read["request_id"] ||
		read["outcome"] != "canceled" || read["final_stage"] != "catchup_follower_wait" {
		t.Fatalf("request cancellation was reattributed: %#v", read)
	}
	if read["project_full_ms"] != "0.000" || read["project_commit_ms"] != "0.000" ||
		owner["flight_id"] != flight.id || owner["outcome"] != "ok" || owner["final_stage"] != "project_commit" {
		t.Fatalf("detached owner/request lifetimes were conflated: request=%#v owner=%#v", read, owner)
	}
	requireHistoryStageMilliseconds(t, read, "catchup_follower_wait_ms")
	requireHistoryStageMilliseconds(t, owner, "commit_ms")
	assertHistoryStagePrivacy(t, lines)
}

func TestTranscriptWebReadStageBreakdownPreservesResponseAndFastReadSilence(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(strconv.FormatBool(slow), func(t *testing.T) {
			logs := captureTranscriptWebStageDiagnostics(t)
			handler := withTranscriptWebReadDiagnostics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if slow {
					diagnostic := r.Context().Value(transcriptWebReadDiagnosticKey{}).(*transcriptWebReadDiagnostic)
					diagnostic.mu.Lock()
					diagnostic.started = time.Now().Add(-2 * time.Second)
					diagnostic.stageStarted = diagnostic.started
					diagnostic.mu.Unlock()
				}
				noteTranscriptWebReadStage(r.Context(), webReadIdentity)
				noteTranscriptWebReadStage(r.Context(), webReadFence)
				// No response-stage label or inferred success for this early error.
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/conversations/fixture/messages", nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("telemetry changed HTTP response: %d", response.Code)
			}
			lines := logs.snapshot()
			if !slow {
				if len(lines) != 0 {
					t.Fatalf("fast completed read no longer silent: %q", lines)
				}
				return
			}
			original := requireHistoryStageLine(t, lines, "history_read_request ")
			stages := requireHistoryStageLine(t, lines, "history_read_stages ")
			if stages["request_id"] != original["request_id"] || stages["outcome"] != "completed" ||
				stages["final_stage"] != "authority_fence" || stages["response_ms"] != "0.000" {
				t.Fatalf("early response was incorrectly relabeled: %#v", stages)
			}
			var total float64
			for key, raw := range stages {
				if strings.HasSuffix(key, "_ms") && key != "elapsed_ms" {
					value, err := strconv.ParseFloat(raw, 64)
					if err != nil || value < 0 {
						t.Fatalf("invalid cumulative stage %q", key)
					}
					total += value
				}
			}
			elapsed, err := strconv.ParseFloat(stages["elapsed_ms"], 64)
			if err != nil || math.Abs(elapsed-total) > 0.016 {
				t.Fatalf("stages do not account for the same interval: total=%v elapsed=%v", total, elapsed)
			}
		})
	}
}

type transcriptWebStageDiagnosticLogs struct {
	mu    sync.Mutex
	lines []string
}

func (capture *transcriptWebStageDiagnosticLogs) Write(value []byte) (int, error) {
	line := string(value)
	if strings.Contains(line, "history_read_") || strings.Contains(line, "history_catchup_") {
		capture.mu.Lock()
		capture.lines = append(capture.lines, line)
		capture.mu.Unlock()
	}
	return len(value), nil
}

func (capture *transcriptWebStageDiagnosticLogs) snapshot() []string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]string(nil), capture.lines...)
}

func captureTranscriptWebStageDiagnostics(t *testing.T) *transcriptWebStageDiagnosticLogs {
	t.Helper()
	capture := &transcriptWebStageDiagnosticLogs{}
	previous := log.Writer()
	log.SetOutput(io.Writer(capture))
	t.Cleanup(func() { log.SetOutput(previous) })
	return capture
}

func requireHistoryStageLine(t *testing.T, lines []string, kind string) map[string]string {
	t.Helper()
	var matches []string
	for _, line := range lines {
		if strings.Contains(line, kind) {
			matches = append(matches, line)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one %s; got %d in %q", kind, len(matches), lines)
	}
	return catchupDiagnosticFields(matches[0])
}

func requireHistoryStageMilliseconds(t *testing.T, fields map[string]string, name string) {
	t.Helper()
	value, err := strconv.ParseFloat(fields[name], 64)
	if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		t.Fatalf("missing or invalid stage %s=%q", name, fields[name])
	}
}

func assertHistoryStagePrivacy(t *testing.T, lines []string) {
	t.Helper()
	for _, line := range lines {
		for _, secret := range []string{"private-stage-error", "/private/", "research", "SELECT", "secret"} {
			if strings.Contains(line, secret) {
				t.Fatalf("stage log leaked %q", secret)
			}
		}
	}
}
