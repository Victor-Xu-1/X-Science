package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTranscriptWebDiagnosticStageTotalsAccumulateUnroundedIntervals(t *testing.T) {
	var timings transcriptWebStageTimings
	// These are exact elapsed intervals, not sleeps or a latency benchmark.
	for _, interval := range []struct {
		stage   transcriptWebReadStage
		elapsed time.Duration
	}{
		{webReadCatchupLeader, 3 * time.Millisecond},
		{webReadFull, 5*time.Millisecond + 500*time.Nanosecond},
		{webReadMetadata, 7 * time.Millisecond},
		{webReadFull, 11*time.Millisecond + 500*time.Nanosecond},
		{webReadCommit, -time.Millisecond},
		{transcriptWebReadStage("private-unknown-stage"), time.Hour},
	} {
		timings.add(interval.stage, interval.elapsed)
	}
	fields := catchupDiagnosticFields(timings.flightFields())
	if fields["lookup_ms"] != "3.000" || fields["full_ms"] != "16.001" ||
		fields["metadata_ms"] != "7.000" || fields["commit_ms"] != "0.000" || len(fields) != 6 {
		t.Fatalf("stage reentry, precision, or fixed fields changed: %#v", fields)
	}
	var total time.Duration
	for _, elapsed := range timings {
		total += elapsed
	}
	if total != 26*time.Millisecond+time.Microsecond {
		t.Fatalf("cumulative time lost elapsed intervals: %v", total)
	}
	request := catchupDiagnosticFields(timings.requestFields())
	if len(request) != 16 || request["project_full_ms"] != "16.001" || request["response_ms"] != "0.000" ||
		strings.Contains(timings.requestFields(), "private") {
		t.Fatalf("request stage schema is not bounded: %#v", request)
	}
}

func TestTranscriptWebStageDiagnosticsConcurrentFinishEmitsOnce(t *testing.T) {
	logs := captureTranscriptWebStageDiagnostics(t)
	ctx, cancel := context.WithCancel(t.Context())
	now := time.Now()
	request := &transcriptWebReadDiagnostic{
		ctx: ctx, id: "0123456789abcdef", route: "history_page", started: now, stageStarted: now,
		stage: webReadCatchupWait, role: "follower", mode: "none",
	}
	flight := newTranscriptWebCatchupDiagnostic(transcriptWebCatchupBackground)
	detached := withTranscriptWebCatchupDiagnostic(context.WithoutCancel(
		context.WithValue(ctx, transcriptWebReadDiagnosticKey{}, request)), flight)
	cancel()
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			noteTranscriptWebReadStage(detached, webReadFull)
			request.finish()
			flight.finish(context.DeadlineExceeded)
			noteTranscriptWebReadStage(detached, webReadCommit)
		})
	}
	workers.Wait()
	lines := logs.snapshot()
	if len(lines) != 4 {
		t.Fatalf("concurrent completion duplicated diagnostics: %q", lines)
	}
	read := requireHistoryStageLine(t, lines, "history_read_stages ")
	owner := requireHistoryStageLine(t, lines, "history_catchup_stages ")
	if read["outcome"] != "canceled" || read["final_stage"] != "catchup_follower_wait" ||
		owner["outcome"] != "deadline" || owner["flight_id"] != flight.id {
		t.Fatalf("concurrent completion changed lifetime identity: request=%#v owner=%#v", read, owner)
	}
	for key := range owner {
		if strings.HasSuffix(key, "_ms") {
			requireHistoryStageMilliseconds(t, owner, key)
		}
	}
}
