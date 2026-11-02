package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"x-science/internal/executionprep"
)

func TestKernelPrepareTimingKeepsLongDurationsAndKnownReuse(t *testing.T) {
	for _, reuse := range []*bool{nil, new(bool), func() *bool { value := true; return &value }()} {
		now := time.Now()
		var records []kernelPrepareDiagnostic
		timing := newAgentKernelPrepareTiming(kernelPrepareDiagnostic{Version: 1, OperationID: "operation", Attempt: 3, RunnerAttempt: 7},
			func() time.Time { return now }, func(record kernelPrepareDiagnostic) { records = append(records, record) })
		for stage := kernelPrepareSource; stage < kernelPreparePhaseCount; stage++ {
			finish := timing.begin(stage)
			now = now.Add(48 * time.Hour)
			finish()
			finish() // A late duplicate cannot count the same span twice.
		}
		timing.bindSession(17, 1, reuse)
		timing.finish("prepared")
		timing.finishReturn(errors.New("private later failure"))
		if len(records) != 1 || len(records[0].Spans) != 5 || records[0].US == nil || *records[0].US != (10*24*time.Hour).Microseconds() {
			t.Fatalf("long preparation was truncated or duplicated: %+v", records)
		}
		got := records[0]
		if got.OperationID != "operation" || got.Attempt != 3 || got.RunnerAttempt != 7 || got.KernelGeneration != 17 || got.BackendGeneration != 1 || got.Outcome != "prepared" {
			t.Fatalf("binding changed: %+v", got)
		}
		if (got.SessionReused == nil) != (reuse == nil) || reuse != nil && *got.SessionReused != *reuse {
			t.Fatalf("reuse was guessed or lost: %+v", got)
		}
	}
}

func TestKernelPrepareTimingSealsFailureOrCancellationBeforeLateCompletion(t *testing.T) {
	for _, cause := range []error{errors.New("private source failure"), context.Canceled} {
		var records []kernelPrepareDiagnostic
		timing := newAgentKernelPrepareTiming(kernelPrepareDiagnostic{Version: 1, OperationID: "operation"}, time.Now,
			func(record kernelPrepareDiagnostic) { records = append(records, record) })
		finish := timing.begin(kernelPrepareSource)
		release, done := make(chan struct{}), make(chan struct{})
		go func() { <-release; finish(); timing.finish("prepared"); close(done) }()
		timing.finishReturn(cause)
		before, _ := json.Marshal(records)
		close(release)
		<-done
		after, _ := json.Marshal(records)
		if len(records) != 1 || string(before) != string(after) || records[0].Outcome == "prepared" || strings.Contains(string(after), "private") {
			t.Fatalf("late completion changed failure diagnostic: %s", after)
		}
	}
}

func TestKernelPrepareTimingUnknownClockDoesNotManufactureZeroDuration(t *testing.T) {
	start := time.Now()
	for _, end := range []time.Time{start.Add(-time.Second), start.AddDate(400, 0, 0)} {
		value, unknown := kernelPrepareElapsed(start, end)
		if value != nil || !unknown {
			t.Fatalf("invalid duration became an exact value: %v %v", value, unknown)
		}
	}
	var legacy *agentKernelPrepareTiming
	legacy.begin(kernelPrepareSource)()
	legacy.finishReturn(context.Canceled)
	if agentKernelPrepareTimingFromContext(nil) != nil || agentKernelPrepareTimingFromContext(context.Background()) != nil {
		t.Fatal("legacy context acquired fabricated timing")
	}
}

type kernelPrepareBarrierSource struct {
	entered chan struct{}
}

func (source kernelPrepareBarrierSource) PrepareExecutionSource(ctx context.Context, request executionprep.Request) (executionprep.Result, error) {
	close(source.entered)
	<-ctx.Done()
	return executionprep.Result{}, errors.New("private source and path must never be copied")
}

func TestKernelPrepareSourceBoundaryObservesCancellationWithoutChangingPreflight(t *testing.T) {
	var records []kernelPrepareDiagnostic
	timing := newAgentKernelPrepareTiming(kernelPrepareDiagnostic{Version: 1, OperationID: "operation"}, time.Now,
		func(record kernelPrepareDiagnostic) { records = append(records, record) })
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), kernelPrepareTimingContextKey{}, timing))
	defer cancel()
	entered, returned := make(chan struct{}), make(chan map[string]any, 1)
	go func() {
		returned <- agentExecutionPreparationPreflight(ctx, "python", map[string]any{"code": "PRIVATE_SCRIPT", "environment": "PRIVATE_ENV"}, nil, kernelPrepareBarrierSource{entered})
	}()
	<-entered
	cancel()
	if result := <-returned; result != nil {
		t.Fatalf("source error changed original preflight result: %v", result)
	}
	timing.finishReturn(ctx.Err())
	raw, _ := json.Marshal(records)
	if len(records) != 1 || len(records[0].Spans) != 1 || records[0].Spans[0].Stage != "source_preparation" || records[0].Outcome != "cancelled_before_prepare" || strings.Contains(string(raw), "PRIVATE") {
		t.Fatalf("wrong source cancellation record: %s", raw)
	}
}

func TestKernelPrepareTimingConcurrentSealPublishesExactlyOnce(t *testing.T) {
	var mu sync.Mutex
	count := 0
	timing := newAgentKernelPrepareTiming(kernelPrepareDiagnostic{Version: 1}, time.Now, func(kernelPrepareDiagnostic) { mu.Lock(); count++; mu.Unlock() })
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() { timing.finish("prepared") })
	}
	workers.Wait()
	if count != 1 {
		t.Fatalf("published %d diagnostics", count)
	}
}

func TestKernelPrepareTimingContextSurvivesOriginalDetachedHandoff(t *testing.T) {
	timing := newAgentKernelPrepareTiming(kernelPrepareDiagnostic{Version: 1}, time.Now, nil)
	request, cancelRequest := context.WithCancel(context.WithValue(context.Background(), kernelPrepareTimingContextKey{}, timing))
	defer cancelRequest()
	setup, cancelSetup := detachedKernelSetupContext(request, 0)
	defer cancelSetup()
	cancelRequest()
	if setup.Err() != nil || agentKernelPrepareTimingFromContext(setup) != timing {
		t.Fatal("timing changed the original detached request handoff")
	}
}
