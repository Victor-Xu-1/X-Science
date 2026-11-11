package server

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
)

func holdContextStartupStore(t *testing.T, store *runtimekv.Store, path, boundary string) func() {
	t.Helper()
	var once sync.Once
	if boundary == "store_mutex" {
		held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- store.EditNamespace("startup-fixture", func(map[string]runtimekv.Entry) (bool, error) {
				close(held)
				<-release
				return false, nil
			})
		}()
		select {
		case <-held:
		case <-time.After(5 * time.Second):
			t.Fatal("fixture did not acquire runtime-store mutex")
		}
		unlock := func() {
			once.Do(func() {
				close(release)
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("release runtime-store fixture: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("runtime-store fixture did not release mutex")
				}
			})
		}
		t.Cleanup(unlock)
		return unlock
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE runtime_state_entries SET version=version WHERE namespace='startup-fixture'`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	release := func() { once.Do(func() { _ = tx.Rollback() }) }
	t.Cleanup(release)
	return release
}

func TestContextUsageStartupYieldsToProviderOnBusyStore(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, boundary := range []string{"store_mutex", "sqlite_writer"} {
			name := "complete/" + boundary
			if streaming {
				name = "stream/" + boundary
			}
			t.Run(name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "runtime.sqlite")
				store := runtimekv.New(path)
				t.Cleanup(func() { _ = store.Close() })
				if _, err := store.Set("startup-fixture", "neighbor", "preserved"); err != nil {
					t.Fatal(err)
				}
				release := holdContextStartupStore(t, store, path, boundary)
				calls := 0
				model := communicationTimingModel{run: func(ctx context.Context, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
					calls++
					return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "provider result"}}, ctx.Err()
				}}
				client := &sessionRunnerDynamicModelClient{
					initialReady: true, initial: sessionRunnerResolvedModelClient{client: model, model: "startup-model"},
					contextUsage: &sessionContextUsageRecorder{store: store, sessionID: "startup", attempt: 1, limitSource: "unknown"},
				}
				type result struct {
					response agentruntime.ModelResponse
					err      error
				}
				done := make(chan result, 1)
				go func() {
					var response agentruntime.ModelResponse
					var err error
					if streaming {
						response, err = client.CompleteStream(t.Context(), agentruntime.ModelRequest{}, nil)
					} else {
						response, err = client.Complete(t.Context(), agentruntime.ModelRequest{})
					}
					done <- result{response: response, err: err}
				}()
				var got result
				select {
				case got = <-done:
				case <-time.After(time.Second):
					t.Error("optional startup observation blocked provider completion while store was held")
					release()
					select {
					case got = <-done:
					case <-time.After(5 * time.Second):
						t.Fatal("model call did not settle after fixture release")
					}
				}
				release()
				if got.err != nil || got.response.Message.Content != "provider result" || calls != 1 {
					t.Fatalf("provider result changed or replayed: response=%+v calls=%d err=%v", got.response, calls, got.err)
				}
				if _, found, err := store.Get(contextUsageNamespace("startup"), "latest"); err != nil || found {
					t.Fatalf("skipped startup observation was committed late: found=%v err=%v", found, err)
				}
				if entry, found, err := store.Get("startup-fixture", "neighbor"); err != nil || !found || entry.Value != "preserved" {
					t.Fatalf("unrelated state changed: entry=%+v found=%v err=%v", entry, found, err)
				}
			})
		}
	}
}

func TestContextUsageStartupHonorsCallerCancellationBeforeDispatch(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "complete"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
			t.Cleanup(func() { _ = store.Close() })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered := make(chan struct{})
			recorder := &sessionContextUsageRecorder{store: store, sessionID: "cancel-startup", limitSource: "unknown"}
			recorder.frameExists = func(probeCtx context.Context, liveSample bool) (bool, error) {
				close(entered)
				if !liveSample {
					t.Error("startup frame check did not use the cancellable probe")
				}
				// Bound the broken implementation's Background context too, so a
				// failing regression releases all test goroutines deterministically.
				select {
				case <-probeCtx.Done():
					return false, probeCtx.Err()
				case <-time.After(time.Second):
					return false, context.DeadlineExceeded
				}
			}
			calls := 0
			model := communicationTimingModel{run: func(ctx context.Context, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
				calls++
				return agentruntime.ModelResponse{}, ctx.Err()
			}}
			client := &sessionRunnerDynamicModelClient{initialReady: true, initial: sessionRunnerResolvedModelClient{client: model, model: "cancel-model"}, contextUsage: recorder}
			done := make(chan error, 1)
			go func() {
				if streaming {
					_, err := client.CompleteStream(ctx, agentruntime.ModelRequest{}, nil)
					done <- err
				} else {
					_, err := client.Complete(ctx, agentruntime.ModelRequest{})
					done <- err
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("model call did not enter startup frame probe")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("startup cancellation = %v", err)
				}
			case <-time.After(200 * time.Millisecond):
				t.Error("startup frame probe ignored caller cancellation")
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("startup call did not settle after bounded fixture probe")
				}
			}
			if calls != 0 {
				t.Errorf("cancelled startup dispatched %d provider calls", calls)
			}
		})
	}
}

func TestContextUsageStartupBudgetIncludesLockedFrameRecheck(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Set("startup-fixture", "neighbor", "preserved"); err != nil {
		t.Fatal(err)
	}
	checks, calls := 0, 0
	var initialProbe context.Context
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "recheck-startup", limitSource: "unknown"}
	recorder.frameExists = func(ctx context.Context, liveSample bool) (bool, error) {
		checks++
		if !liveSample {
			t.Error("startup frame recheck did not use bounded probe")
		}
		if checks == 1 {
			initialProbe = ctx
			return true, nil
		}
		if ctx != initialProbe {
			t.Error("startup frame checks did not share one budget")
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Second):
			return false, context.DeadlineExceeded
		}
	}
	model := communicationTimingModel{run: func(ctx context.Context, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
		calls++
		return agentruntime.ModelResponse{Message: agentruntime.Message{Content: "provider result"}}, ctx.Err()
	}}
	client := &sessionRunnerDynamicModelClient{initialReady: true, initial: sessionRunnerResolvedModelClient{client: model, model: "recheck-model"}, contextUsage: recorder}
	started := time.Now()
	response, err := client.CompleteStream(t.Context(), agentruntime.ModelRequest{}, nil)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond || err != nil || response.Message.Content != "provider result" || calls != 1 || checks != 2 {
		t.Fatalf("frame recheck blocked or altered provider: elapsed=%v calls=%d checks=%d err=%v", elapsed, calls, checks, err)
	}
	if entries, err := store.List(contextUsageNamespace(recorder.sessionID)); err != nil || len(entries) != 0 {
		t.Fatalf("timed-out frame recheck committed observation: entries=%+v err=%v", entries, err)
	}
}

func TestContextUsageFinishYieldsToProviderResultOnBusyStore(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			name := "complete"
			if streaming {
				name = "stream"
			}
			if failed {
				name += "/failure"
			} else {
				name += "/success"
			}
			t.Run(name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "runtime.sqlite")
				store := runtimekv.New(path)
				t.Cleanup(func() { _ = store.Close() })
				if _, err := store.Set("finish-fixture", "neighbor", "preserved"); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				providerStarted, allowReturn, providerReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
				calls := 0
				var providerErr error
				if failed {
					providerErr = context.Canceled
				}
				model := communicationTimingModel{run: func(ctx context.Context, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
					calls++
					close(providerStarted) // The request observation has committed.
					select {
					case <-allowReturn:
					case <-ctx.Done():
						return agentruntime.ModelResponse{}, ctx.Err()
					}
					defer close(providerReturned)
					return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "provider result"},
						Usage: agentruntime.ModelUsage{InputTokens: 20, OutputTokens: 4, TotalTokens: 24}}, providerErr
				}}
				client := &sessionRunnerDynamicModelClient{
					initialReady: true, initial: sessionRunnerResolvedModelClient{client: model, model: "finish-model"},
					contextUsage: &sessionContextUsageRecorder{store: store, sessionID: "finish", attempt: 1, limitSource: "unknown"},
				}
				type result struct {
					response agentruntime.ModelResponse
					err      error
				}
				done := make(chan result, 1)
				go func() {
					var response agentruntime.ModelResponse
					var err error
					request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "inspect"}}}
					if streaming {
						response, err = client.CompleteStream(ctx, request, nil)
					} else {
						response, err = client.Complete(ctx, request)
					}
					done <- result{response: response, err: err}
				}()
				select {
				case <-providerStarted:
				case <-time.After(5 * time.Second):
					t.Fatal("provider did not start after its request observation")
				}
				before := readContextUsageTest(t, store, "finish")
				if before.State != "request" || before.Source != "estimated" {
					t.Fatalf("initial request observation = %+v", before)
				}
				release := holdContextStartupStore(t, store, path, "store_mutex")
				defer release()
				close(allowReturn)
				select {
				case <-providerReturned:
				case <-time.After(5 * time.Second):
					t.Fatal("fixture did not release the provider result")
				}
				var got result
				select {
				case got = <-done:
				case <-time.After(time.Second):
					t.Error("optional terminal observation blocked an already returned provider result")
					release()
					select {
					case got = <-done:
					case <-time.After(5 * time.Second):
						t.Fatal("model call did not settle after terminal fixture release")
					}
				}
				release()
				if calls != 1 || !errors.Is(got.err, providerErr) || !failed && got.response.Message.Content != "provider result" || failed && got.response.Message.Content != "" {
					t.Fatalf("provider outcome changed or replayed: calls=%d response=%+v err=%v", calls, got.response, got.err)
				}
				latest, history, found, err := readRunnerContextUsage(t.Context(), store, "finish")
				if err != nil || !found || latest.RequestID != before.RequestID || latest.State != "request" || latest.Source != "estimated" || latest.UsedTokens != before.UsedTokens || latest.CallUsage != nil || len(history.Samples) != 1 || history.Samples[0].State != "request" {
					t.Fatalf("timed-out terminal observation committed late or invented measurement: latest=%+v history=%+v found=%v err=%v", latest, history, found, err)
				}
			})
		}
	}
}
