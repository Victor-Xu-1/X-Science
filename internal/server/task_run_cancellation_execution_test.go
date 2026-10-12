package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	taskruns "x-science/internal/persistence/taskruns"
)

type taskRunCancelTransport func(*http.Request) (*http.Response, error)

func (f taskRunCancelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTaskRunCancellationServer(t *testing.T, transport http.RoundTripper) *Server {
	t.Helper()
	srv := New(Options{FileRoot: t.TempDir(), HTTPClient: &http.Client{Transport: transport}})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Close(ctx); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return srv
}
func startTaskRunCancellationFixture(t *testing.T, srv *Server, runID string) taskruns.Record {
	t.Helper()
	value, err := srv.executeTaskRunTool(map[string]any{"action": "start", "run_id": runID, "objective": "Return a short controlled answer", "task_graph": explicitTaskRunTestGraph("execute")})
	if err != nil {
		t.Fatal(err)
	}
	return value.(taskruns.Record)
}
func makeTaskRunCancellationQueueLegacy(t *testing.T, srv *Server, sessionID string) {
	t.Helper()
	entries, err := srv.eventJournal.ReadAllStrict(sessionID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("legacy fixture source queue entries=%d err=%v", len(entries), err)
	}
	entry := entries[0]
	delete(mapValue(entry.Message["runnerRuntime"]), "taskRunInstanceId")
	if _, err := srv.eventJournal.TruncateAfter(sessionID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.eventJournal.Append(sessionID, entry.Message, eventjournal.Metadata{RunID: entry.RunID, ClientMessageID: entry.ClientMessageID}); err != nil {
		t.Fatal(err)
	}
	entries, err = srv.eventJournal.ReadAllStrict(sessionID)
	if err != nil || len(entries) != 1 || entries[0].RunID != entry.RunID || entries[0].ClientMessageID != entry.ClientMessageID || stringValue(mapValue(entries[0].Message["runnerRuntime"])["taskRunInstanceId"]) != "" {
		t.Fatalf("legacy fixture queue metadata=%#v err=%v", entries, err)
	}
}
func taskRunCancellationClaim(t *testing.T, srv *Server, sessionID string) (sessionstore.Session, sessionstore.RunnerMutationClaim) {
	t.Helper()
	session, claimed, err := srv.sessionStore.ClaimRunner(sessionID, "controlled-runner", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%t err=%v session=%#v", claimed, err, session)
	}
	return session, sessionstore.RunnerClaimFromSession(session)
}
func bindTaskRunCancellationFixture(t *testing.T, srv *Server, session sessionstore.Session) (context.Context, *activeSessionRun) {
	t.Helper()
	ctx, active, cleanup, err := srv.registerActiveSessionRun(context.Background(), session.ID, session.Runner.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	active.projectionClaim = sessionstore.RunnerClaimFromSession(session)
	entries, err := srv.eventJournal.ReadAll(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.bindTaskRunSessionInput(ctx, session, entries); err != nil {
		t.Fatal(err)
	}
	return ctx, active
}
func taskRunFinishRecoverySettlement(t *testing.T, srv *Server, session sessionstore.Session) *taskRunCancellationSettlement {
	t.Helper()
	_, active := bindTaskRunCancellationFixture(t, srv, session)
	active.settlement.Lock()
	defer active.settlement.Unlock()
	option := taskRunCancellationSettlementLocked(active, sessionstore.RunnerClaimFromSession(session))
	if option == nil || option.instance == "" {
		t.Fatal("missing actual queued instance binding")
	}
	return option
}
func cancelTaskRunFixture(t *testing.T, srv *Server, run taskruns.Record) taskruns.Record {
	t.Helper()
	value, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID})
	if err != nil {
		t.Fatal(err)
	}
	return value.(taskruns.Record)
}
func taskRunFinishEntry(t *testing.T, srv *Server, result SessionRunnerCycleResult) eventjournal.Entry {
	t.Helper()
	entries, err := srv.eventJournal.ReadAll(result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.EventID == result.FinishEventID {
			return entry
		}
	}
	t.Fatalf("missing finish %+v entries=%#v", result, entries)
	return eventjournal.Entry{}
}
func taskRunWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("controlled execution did not reach boundary")
	}
}

func TestTaskRunCancellationNativeActualSettlement(t *testing.T) {
	for _, mode := range []string{"completed", "failed", "cancelled", "cancelled_new_input"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			var once sync.Once
			var requests atomic.Int64
			srv := newTaskRunCancellationServer(t, taskRunCancelTransport(func(r *http.Request) (*http.Response, error) {
				requests.Add(1)
				once.Do(func() { close(started) })
				if strings.HasPrefix(mode, "cancelled") {
					<-r.Context().Done()
				}
				status, body := http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"Controlled answer completed."}}]}`
				if mode == "failed" {
					status, body = http.StatusOK, `{"choices":[]}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			}))
			run := startTaskRunCancellationFixture(t, srv, "")
			type outcome struct {
				result SessionRunnerCycleResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := srv.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: "native-controlled", Endpoint: "http://controlled.invalid/v1/chat/completions", Model: "controlled", LeaseTTL: time.Minute, MaxToolRounds: 1, MaxAttempts: 1, DisableSkillDiscovery: true, DisableMCPDiscovery: true})
				done <- outcome{result, err}
			}()
			if strings.HasPrefix(mode, "cancelled") {
				taskRunWait(t, started)
				if mode == "cancelled_new_input" {
					if _, err := srv.appendSessionToolEvent(map[string]any{"sessionId": run.SessionID, "role": "user", "message": map[string]any{"type": "message", "text": "A later ordinary input"}, "clientMessageId": "later-normal"}); err != nil {
						t.Fatal(err)
					}
				}
				cancelled := cancelTaskRunFixture(t, srv, run)
				if cancelled.Status != "cancelled" || len(cancelled.ActiveChildren) != 0 {
					t.Fatalf("cancelled graph=%#v", cancelled)
				}
				cancelTaskRunFixture(t, srv, run)
			}
			var actual outcome
			select {
			case actual = <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("native cycle did not settle")
			}
			want := mode
			if strings.HasPrefix(want, "cancelled") {
				want = "cancelled"
			}
			if actual.err != nil || !actual.result.Claimed || actual.result.Status != want || requests.Load() != 1 {
				t.Fatalf("result=%+v requests=%d err=%v", actual.result, requests.Load(), actual.err)
			}
			finish := taskRunFinishEntry(t, srv, actual.result)
			if stringValue(finish.Message["taskRunInstanceId"]) != taskruns.ExecutionInstance(run) || stringValue(finish.Message["status"]) != want {
				t.Fatalf("finish=%#v", finish)
			}
			if srv.hasActiveSessionRun(run.SessionID) {
				t.Fatal("actual cycle retained registered handle")
			}
			session, _, err := srv.sessionStore.Get(run.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancelled_new_input" {
				if session.Runner != nil || session.LastRole != "user" {
					t.Fatalf("later input hidden by cancelled finish: %#v", session)
				}
				next, claimed, err := srv.sessionStore.ClaimNextRunner("next-input", time.Minute)
				if err != nil || !claimed || next.ID != run.SessionID {
					t.Fatalf("later input claim=%t next=%#v err=%v", claimed, next, err)
				}
			} else if session.Runner == nil || session.Runner.Status != want {
				t.Fatalf("durable runner=%#v", session.Runner)
			}
			if want == "cancelled" {
				if actual.result.AssistantEventID != 0 || actual.result.AutoAdvanced {
					t.Fatalf("cancelled output/advance=%+v", actual.result)
				}
				persisted, _, err := srv.taskRunStore.Get(run.RunID)
				if err != nil || persisted.Status != "cancelled" {
					t.Fatalf("graph=%#v err=%v", persisted, err)
				}
				for _, namespace := range []string{goalRunLedgerRuntimeNamespace, goalRunActiveRuntimeNamespace} {
					items, err := srv.runtimeStore.List(namespace)
					if err != nil || len(items) != 0 {
						t.Fatalf("cancelled ledger %s=%#v err=%v", namespace, items, err)
					}
				}
			}
		})
	}
}

func TestTaskRunCancellationCommandActualSettlement(t *testing.T) {
	for _, mode := range []string{"completed", "failed", "cancelled", "before_claim"} {
		t.Run(mode, func(t *testing.T) {
			srv := newTaskRunCancellationServer(t, nil)
			run := startTaskRunCancellationFixture(t, srv, "")
			started := make(chan struct{})
			var calls atomic.Int64
			if mode == "before_claim" {
				cancelTaskRunFixture(t, srv, run)
			}
			executor := func(ctx context.Context, _ SessionRunnerCommandOptions, input sessionRunnerCommandInput) (sessionRunnerCommandOutput, error) {
				calls.Add(1)
				close(started)
				if mode == "cancelled" {
					<-ctx.Done()
					return sessionRunnerCommandOutput{Status: "completed", AssistantMessage: "late output must be suppressed"}, nil
				}
				if mode == "failed" {
					return sessionRunnerCommandOutput{}, errors.New("controlled command failure")
				}
				if input.Session.ID != run.SessionID || len(input.Entries) == 0 {
					return sessionRunnerCommandOutput{}, errors.New("wrong actual command replay")
				}
				return sessionRunnerCommandOutput{Status: "completed", AssistantMessage: "controlled command answer"}, nil
			}
			type outcome struct {
				result SessionRunnerCycleResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := srv.runSessionRunnerCommandOnce(context.Background(), SessionRunnerCommandOptions{RunnerID: "command-controlled", Command: "synthetic-not-executed", LeaseTTL: time.Minute}, executor)
				done <- outcome{result, err}
			}()
			if mode == "cancelled" {
				taskRunWait(t, started)
				cancelTaskRunFixture(t, srv, run)
			}
			var actual outcome
			select {
			case actual = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("command cycle did not settle")
			}
			want := mode
			wantCalls := int64(1)
			if mode == "before_claim" {
				want = "cancelled"
				wantCalls = 0
			}
			if actual.err != nil || actual.result.Status != want || calls.Load() != wantCalls {
				t.Fatalf("result=%+v bodyCalls=%d err=%v", actual.result, calls.Load(), actual.err)
			}
			finish := taskRunFinishEntry(t, srv, actual.result)
			if stringValue(finish.Message["taskRunInstanceId"]) != taskruns.ExecutionInstance(run) {
				t.Fatalf("command finish instance=%#v", finish)
			}
			if want == "cancelled" && (actual.result.AssistantEventID != 0 || actual.result.AutoAdvanced) {
				t.Fatalf("cancelled command published late output: %+v", actual.result)
			}
			if mode == "before_claim" {
				entries, _ := srv.eventJournal.ReadAll(run.SessionID)
				for _, entry := range entries {
					if stringValue(entry.Message["type"]) == "runner_checkpoint" {
						t.Fatal("cancel-before-claim crossed command checkpoint/body boundary")
					}
				}
			}
		})
	}
}

func TestTaskRunCancellationCurrentBindingAndInstanceIsolation(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	run := startTaskRunCancellationFixture(t, srv, "reused")
	session, _ := taskRunCancellationClaim(t, srv, run.SessionID)
	ctx, active := bindTaskRunCancellationFixture(t, srv, session)
	normal, err := srv.appendSessionToolEvent(map[string]any{"sessionId": run.SessionID, "role": "user", "message": map[string]any{"text": "ordinary new input"}, "clientMessageId": "normal-rebind"})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := srv.eventJournal.ReadAll(run.SessionID)
	if err := srv.bindTaskRunSessionInput(ctx, session, entries); err != nil {
		t.Fatal(err)
	}
	if active.inputBinding.userEventID != normal.EventID || active.inputBinding.verified {
		t.Fatalf("actual ordinary binding=%#v", active.inputBinding)
	}
	cancelTaskRunFixture(t, srv, run)
	if context.Cause(ctx) != nil {
		t.Fatalf("TaskRun cancellation stopped ordinary input: %v", context.Cause(ctx))
	}
	// A same-session replacement is distinguished by its queue receipt, not by its reused name.
	replacement, err := srv.taskRunStore.Create(taskruns.Input{Action: "start", RunID: run.RunID, Objective: "replacement", TaskGraph: &taskruns.Graph{Steps: []taskruns.StepInput{{ID: "new", Title: "New", Executor: map[string]any{"kind": "agent"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err = srv.queueTaskRunSession(replacement, "new instance")
	if err != nil {
		t.Fatal(err)
	}
	entries, _ = srv.eventJournal.ReadAll(run.SessionID)
	if err := srv.bindTaskRunSessionInput(ctx, session, entries); err != nil {
		t.Fatal(err)
	}
	srv.stopTaskRunSessionInputs(run)
	if context.Cause(ctx) != nil {
		t.Fatalf("old instance stopped replacement: %v", context.Cause(ctx))
	}
	srv.stopTaskRunSessionInputs(replacement)
	if !errors.Is(context.Cause(ctx), ErrGenerationStopped) {
		t.Fatalf("matching replacement receipt not stopped: %v", context.Cause(ctx))
	}
	if err := srv.bindTaskRunSessionInput(ctx, session, entries[:1]); !errors.Is(err, ErrGenerationStopped) {
		t.Fatalf("cancelled handle rebound: %v", err)
	}
}

func TestTaskRunCancellationCommitFailureSignalBoundary(t *testing.T) {
	for _, afterJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("after_json_%t", afterJSON), func(t *testing.T) {
			srv := newTaskRunCancellationServer(t, nil)
			run := startTaskRunCancellationFixture(t, srv, "")
			session, _ := taskRunCancellationClaim(t, srv, run.SessionID)
			ctx, _ := bindTaskRunCancellationFixture(t, srv, session)
			if afterJSON {
				if err := os.Remove(run.OutputPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(run.OutputPath, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(srv.fileRoot, "task_runs", run.RunID+".json.tmp"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			value, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID})
			if err == nil || errors.Is(context.Cause(ctx), ErrGenerationStopped) != afterJSON {
				t.Fatalf("value=%#v err=%v cause=%v", value, err, context.Cause(ctx))
			}
			if afterJSON {
				if value.(taskruns.Record).Status != "cancelled" {
					t.Fatalf("postcommit response=%#v", value)
				}
			}
		})
	}
}

func TestTaskRunCancellationLegacyExecutionAndUnconfirmedReport(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	run := startTaskRunCancellationFixture(t, srv, "")
	delete(run.Orchestration, "taskRunInstanceId")
	if _, err := srv.taskRunStore.Write(run); err != nil {
		t.Fatal(err)
	}
	makeTaskRunCancellationQueueLegacy(t, srv, run.SessionID)
	session, _ := taskRunCancellationClaim(t, srv, run.SessionID)
	ctx, active, cleanup, err := srv.registerActiveSessionRun(context.Background(), session.ID, session.Runner.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	entries, err := srv.eventJournal.ReadAllStrict(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A truly historical receipt has no instance. Its ordinary execution remains permitted.
	runtime := mapValue(entries[0].Message["runnerRuntime"])
	delete(runtime, "taskRunInstanceId")
	if err := srv.bindTaskRunSessionInput(ctx, session, entries); err != nil {
		t.Fatalf("legacy execution blocked: %v", err)
	}
	if active.inputBinding.verified {
		t.Fatal("empty legacy identity asserted as receipt proof")
	}
	cancelled := cancelTaskRunFixture(t, srv, run)
	if context.Cause(ctx) != nil || stringValue(mapValue(cancelled.Orchestration["cancellationPropagation"])["state"]) != "unconfirmed" || !strings.Contains(cancelled.NextActions[0], "Stop generation") {
		t.Fatalf("legacy report=%#v cause=%v", cancelled, context.Cause(ctx))
	}
	markdown, err := os.ReadFile(cancelled.OutputPath)
	if err != nil || !strings.Contains(string(markdown), "cannot be confirmed safely") {
		t.Fatalf("legacy markdown=%q err=%v", markdown, err)
	}
}

func TestTaskRunCancellationMirrorInstanceMatrix(t *testing.T) {
	for _, pair := range []struct {
		name, graph, event string
		match              bool
	}{{"legacy_empty", "", "", true}, {"missing_to_new", "new", "", false}, {"new_to_legacy", "", "new", false}, {"matching", "new", "new", true}, {"replacement", "new", "old", false}} {
		t.Run(pair.name, func(t *testing.T) {
			srv := newTaskRunCancellationServer(t, nil)
			run := startTaskRunCancellationFixture(t, srv, "")
			if pair.graph == "" {
				delete(run.Orchestration, "taskRunInstanceId")
			} else {
				run.Orchestration["taskRunInstanceId"] = pair.graph
			}
			if _, err := srv.taskRunStore.Write(run); err != nil {
				t.Fatal(err)
			}
			before, _, _ := srv.taskRunStore.Get(run.RunID)
			entry, err := srv.eventJournal.Append(run.SessionID, eventjournal.Message{"type": "runner_finished", "role": "system", "status": "completed", "taskRunInstanceId": pair.event}, eventjournal.Metadata{RunID: run.RunID})
			if err != nil {
				t.Fatal(err)
			}
			if err := srv.mirrorTaskRunRunnerEvent(run.SessionID, entry); err != nil {
				t.Fatal(err)
			}
			after, _, _ := srv.taskRunStore.Get(run.RunID)
			if reflect.DeepEqual(before, after) == pair.match {
				t.Fatalf("wrong instance consumer matrix before=%#v after=%#v", before, after)
			}
			entries, err := srv.eventJournal.ReadAll(run.SessionID)
			if err != nil || entries[len(entries)-1].EventID != entry.EventID {
				t.Fatalf("truthful finish was lost: entries=%#v err=%v", entries, err)
			}
		})
	}
}

func TestTaskRunCancellationDelayedReadAndRegistrySnapshotRevalidateCurrentInput(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	run := startTaskRunCancellationFixture(t, srv, "")
	session, _ := taskRunCancellationClaim(t, srv, run.SessionID)
	ctx, active := bindTaskRunCancellationFixture(t, srv, session)
	oldBinding := active.inputBinding
	normal, err := srv.appendSessionToolEvent(map[string]any{"sessionId": session.ID, "role": "user", "message": map[string]any{"text": "new ordinary input"}, "clientMessageId": "snapshot-normal"})
	if err != nil {
		t.Fatal(err)
	}
	active.settlement.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	var cancelled taskruns.Record
	for {
		current, _, err := srv.taskRunStore.Get(run.RunID)
		if err != nil {
			active.settlement.Unlock()
			t.Fatal(err)
		}
		if current.Status == "cancelled" {
			cancelled = current
			break
		}
		if time.Now().After(deadline) {
			active.settlement.Unlock()
			t.Fatal("cancel did not commit before blocked signal")
		}
		time.Sleep(time.Millisecond)
	}
	// The cancellation pointer snapshot may contain this handle, but its input has changed before signalling.
	active.inputBinding = sessionRunInputBinding{userEventID: normal.EventID}
	active.settlement.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel signal deadlocked")
	}
	if context.Cause(ctx) != nil {
		t.Fatalf("stale registry pointer stopped normal input: %v", context.Cause(ctx))
	}
	// The graph read was started for Q; checking its result after N bound must also be harmless.
	if err := cancelTaskRunBindingIfCurrent(ctx, active, oldBinding, cancelled); err != nil || context.Cause(ctx) != nil {
		t.Fatalf("delayed graph read cancelled new input: err=%v cause=%v", err, context.Cause(ctx))
	}
}

func TestTaskRunCancellationMultipleChildrenAndSettledControl(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	run := startTaskRunCancellationFixture(t, srv, "")
	var contexts []context.Context
	for index := range 3 {
		child := run
		child.SessionID = fmt.Sprintf("taskrun:child-%d", index)
		if err := srv.sessionStore.Upsert(sessionstore.Session{ID: child.SessionID}); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.appendSessionToolEvent(map[string]any{"sessionId": child.SessionID, "role": "user", "runId": run.RunID, "clientMessageId": fmt.Sprintf("child-%d", index), "message": map[string]any{"taskRun": run.RunID, "text": "same graph child", "runnerRuntime": taskRunRunnerRuntimeMetadata(child)}}); err != nil {
			t.Fatal(err)
		}
		session, _ := taskRunCancellationClaim(t, srv, child.SessionID)
		ctx, active := bindTaskRunCancellationFixture(t, srv, session)
		if index == 2 {
			active.settlement.Lock()
			active.settled = true
			active.settlement.Unlock()
		}
		contexts = append(contexts, ctx)
	}
	other := startTaskRunCancellationFixture(t, srv, "")
	otherSession, _ := taskRunCancellationClaim(t, srv, other.SessionID)
	otherCtx, _ := bindTaskRunCancellationFixture(t, srv, otherSession)
	cancelTaskRunFixture(t, srv, run)
	for index, ctx := range contexts {
		if errors.Is(context.Cause(ctx), ErrGenerationStopped) != (index < 2) {
			t.Fatalf("child %d cause=%v", index, context.Cause(ctx))
		}
	}
	if context.Cause(otherCtx) != nil {
		t.Fatalf("unrelated TaskRun stopped: %v", context.Cause(otherCtx))
	}
}

func TestTaskRunCancellationCompactionUsesEachActualInputSnapshot(t *testing.T) {
	for _, mode := range []string{"cancel_precompact", "normal_before_compact_read", "cancel_during_summary", "normal_after_compact"} {
		t.Run(mode, func(t *testing.T) {
			pre := make(chan struct{})
			summary := make(chan struct{})
			post := make(chan struct{})
			native := make(chan struct{})
			releasePre := make(chan struct{})
			releaseSummary := make(chan struct{})
			releasePost := make(chan struct{})
			releaseNative := make(chan struct{})
			var summaryCalls, nativeCalls atomic.Int64
			transport := taskRunCancelTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"decision":"allow"}`
				switch r.URL.Path {
				case "/pre":
					close(pre)
					if mode == "cancel_precompact" {
						<-r.Context().Done()
						return nil, r.Context().Err()
					}
					if mode == "normal_before_compact_read" {
						select {
						case <-releasePre:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
					}
				case "/summary":
					summaryCalls.Add(1)
					close(summary)
					if mode == "cancel_during_summary" {
						<-r.Context().Done()
						return nil, r.Context().Err()
					}
					if mode == "normal_before_compact_read" {
						select {
						case <-releaseSummary:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
					}
					body = `{"choices":[{"message":{"role":"assistant","content":"Durable controlled summary including pending ordinary input."}}]}`
				case "/post":
					close(post)
					if mode == "normal_after_compact" {
						select {
						case <-releasePost:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
					}
				case "/native":
					nativeCalls.Add(1)
					close(native)
					if mode == "normal_after_compact" {
						select {
						case <-releaseNative:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
					}
					body = `{"choices":[{"message":{"role":"assistant","content":"Ordinary input completed."}}]}`
				default:
					return nil, fmt.Errorf("unexpected controlled transport %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			srv := newTaskRunCancellationServer(t, transport)
			run := startTaskRunCancellationFixture(t, srv, "")
			srv.compactSummarizer = SessionRunnerChatOptions{Endpoint: "http://controlled.invalid/summary", Model: "controlled", MaxAttempts: 1}
			if _, err := srv.settingsStore.Set(configStoreKey("autoCompactTokenThreshold"), 1); err != nil {
				t.Fatal(err)
			}
			if _, err := srv.settingsStore.Set("hooks", map[string]any{"PreCompact": []any{map[string]any{"matcher": "auto", "hooks": []any{map[string]any{"type": "http", "url": "http://controlled.invalid/pre"}}}}, "PostCompact": []any{map[string]any{"matcher": "auto", "hooks": []any{map[string]any{"type": "http", "url": "http://controlled.invalid/post"}}}}}); err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				result SessionRunnerCycleResult
				err    error
			}
			done := make(chan outcome, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			go func() {
				result, err := srv.RunSessionRunnerChatOnce(ctx, SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: "compact-controlled", Endpoint: "http://controlled.invalid/native", Model: "controlled", LeaseTTL: time.Minute, MaxToolRounds: 1, MaxAttempts: 1, DisableSkillDiscovery: true, DisableMCPDiscovery: true})
				done <- outcome{result, err}
			}()
			boundary := pre
			if mode == "cancel_during_summary" {
				boundary = summary
			}
			if mode == "normal_after_compact" {
				boundary = post
			}
			select {
			case <-boundary:
			case actual := <-done:
				t.Fatalf("cycle exited before compact boundary: result=%+v err=%v", actual.result, actual.err)
			case <-time.After(10 * time.Second):
				t.Fatal("compact boundary not reached")
			}
			if mode != "cancel_precompact" {
				if _, err := srv.appendSessionToolEvent(map[string]any{"sessionId": run.SessionID, "role": "user", "clientMessageId": "compact-normal", "message": map[string]any{"text": "A new ordinary request"}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "normal_before_compact_read" {
				close(releasePre)
				taskRunWait(t, summary)
			}
			if mode == "normal_after_compact" {
				close(releasePost)
				taskRunWait(t, native)
			}
			cancelTaskRunFixture(t, srv, run)
			if mode == "normal_before_compact_read" {
				close(releaseSummary)
			}
			if mode == "normal_after_compact" {
				close(releaseNative)
			}
			var actual outcome
			select {
			case actual = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("compaction cycle did not settle")
			}
			want := "cancelled"
			if strings.HasPrefix(mode, "normal_") {
				want = "completed"
			}
			if actual.err != nil || actual.result.Status != want {
				t.Fatalf("result=%+v summary=%d native=%d err=%v", actual.result, summaryCalls.Load(), nativeCalls.Load(), actual.err)
			}
			if mode == "cancel_precompact" && (summaryCalls.Load() != 0 || nativeCalls.Load() != 0) {
				t.Fatal("cancel crossed compaction consuming boundary")
			}
			if mode == "cancel_during_summary" && nativeCalls.Load() != 0 {
				t.Fatal("cancelled context rebound later normal input")
			}
			finish := taskRunFinishEntry(t, srv, actual.result)
			if want == "completed" && stringValue(finish.Message["taskRunInstanceId"]) != "" {
				t.Fatalf("ordinary input mislabelled TaskRun: %#v", finish)
			}
			graph, _, _ := srv.taskRunStore.Get(run.RunID)
			if graph.Status != "cancelled" {
				t.Fatalf("graph advanced after cancel: %#v", graph)
			}
			if mode == "cancel_during_summary" {
				session, _, _ := srv.sessionStore.Get(run.SessionID)
				if session.Runner != nil || session.LastRole != "user" {
					t.Fatalf("new input lost after cancelled summary: %#v", session)
				}
			}
		})
	}
}

func TestTaskRunCancellationPublicReceiptCompletionRestartAndEvidence(t *testing.T) {
	for _, entryPoint := range []string{"next", "pick", "claim"} {
		for _, status := range []string{"completed", "failed", "cancelled"} {
			t.Run(entryPoint+"_"+status, func(t *testing.T) {
				srv := newTaskRunCancellationServer(t, nil)
				run := startTaskRunCancellationFixture(t, srv, "")
				input := map[string]any{"sessionId": run.SessionID, "runnerId": "public-controlled", "limit": 20, "ttlSeconds": 120}
				var admission map[string]any
				var err error
				switch entryPoint {
				case "next":
					admission, err = srv.nextSessionRunner(input)
				case "pick":
					admission, err = srv.pickSessionRunner(input)
				case "claim":
					admission, err = srv.claimSessionRunner(input)
				}
				if err != nil || !boolValue(admission["claimed"], false) {
					t.Fatalf("public admission=%#v err=%v", admission, err)
				}
				session := admission["session"].(sessionstore.Session)
				claim := sessionstore.RunnerClaimFromSession(session)
				if session.Runner.InputReceipt == nil || session.Runner.InputReceipt.TaskRunInstanceID != taskruns.ExecutionInstance(run) {
					t.Fatalf("public receipt=%#v", session.Runner.InputReceipt)
				}
				assistant, err := srv.appendSessionToolEvent(map[string]any{"sessionId": run.SessionID, "runnerId": claim.RunnerID, "runnerAttempt": claim.Attempt, "claimToken": claim.ClaimToken, "role": "assistant", "clientMessageId": "public-assistant", "message": map[string]any{"text": "The exact public claim output"}})
				if err != nil {
					t.Fatal(err)
				}
				// Later unowned assistant text must not become this runner's completion evidence.
				if _, err := srv.appendSessionToolEvent(map[string]any{"sessionId": run.SessionID, "role": "assistant", "clientMessageId": "unowned-assistant", "message": map[string]any{"text": "Unrelated evidence must be excluded"}}); err != nil {
					t.Fatal(err)
				}
				restarted := New(Options{FileRoot: srv.fileRoot})
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					_ = restarted.Close(ctx)
				})
				finishInput := map[string]any{"sessionId": run.SessionID, "runnerId": claim.RunnerID, "runnerAttempt": claim.Attempt, "claimToken": claim.ClaimToken, "status": status, "message": "public finish", "clientMessageId": "public-finish", "taskRunInstanceId": "caller-forged"}
				result, err := restarted.finishSessionRunner(finishInput)
				if err != nil {
					t.Fatal(err)
				}
				entry := result["event"].(*eventjournal.Entry)
				if stringValue(entry.Message["taskRunInstanceId"]) != taskruns.ExecutionInstance(run) || numberValue(entry.Message["taskRunInputEventId"]) >= assistant.EventID {
					t.Fatalf("public finish lacked source receipt: %#v", entry)
				}
				if _, err := restarted.finishSessionRunner(finishInput); err != nil {
					t.Fatalf("public retry: %v", err)
				}
				reconciled, err := restarted.reconcileTaskRun(run.RunID)
				if err != nil || reconciled.Status != status {
					t.Fatalf("public graph=%#v err=%v", reconciled, err)
				}
				if status == "completed" && (len(reconciled.EvidenceIndex) != 1 || reconciled.EvidenceIndex[0].Summary != "The exact public claim output") {
					t.Fatalf("public bounded evidence=%#v", reconciled.EvidenceIndex)
				}
			})
		}
	}
}

func TestTaskRunCancellationPublicReturnedNormalInputDoesNotSettleTaskRun(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	run := startTaskRunCancellationFixture(t, srv, "")
	normal, err := srv.appendSessionToolEvent(map[string]any{"sessionId": run.SessionID, "role": "user", "clientMessageId": "public-normal", "message": map[string]any{"text": "A separate normal request"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := srv.nextSessionRunner(map[string]any{"sessionId": run.SessionID, "runnerId": "public-normal-runner", "limit": 20})
	if err != nil {
		t.Fatal(err)
	}
	session := result["session"].(sessionstore.Session)
	claim := sessionstore.RunnerClaimFromSession(session)
	if receipt := session.Runner.InputReceipt; receipt == nil || receipt.UserEventID != normal.EventID || receipt.TaskRunInstanceID != "" {
		t.Fatalf("normal returned receipt=%#v", receipt)
	}
	finished, err := srv.finishSessionRunner(map[string]any{"sessionId": run.SessionID, "runnerId": claim.RunnerID, "runnerAttempt": claim.Attempt, "claimToken": claim.ClaimToken, "status": "completed", "clientMessageId": "normal-finish"})
	if err != nil {
		t.Fatal(err)
	}
	if finished["taskRunProjection"] != nil || stringValue(finished["event"].(*eventjournal.Entry).Message["taskRunInstanceId"]) != "" {
		t.Fatalf("known normal mistaken for unknown/TaskRun: %#v", finished)
	}
	graph, err := srv.reconcileTaskRun(run.RunID)
	if err != nil || graph.Status != "running" || len(graph.EvidenceIndex) != 0 {
		t.Fatalf("normal finish settled Q: %#v err=%v", graph, err)
	}
}

func TestTaskRunCancellationPublicOldReceiptCannotSettleReplacement(t *testing.T) {
	for _, oldLegacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("old_legacy_%t", oldLegacy), func(t *testing.T) {
			srv := newTaskRunCancellationServer(t, nil)
			run := startTaskRunCancellationFixture(t, srv, "reused-public")
			if oldLegacy {
				delete(run.Orchestration, "taskRunInstanceId")
				if _, err := srv.taskRunStore.Write(run); err != nil {
					t.Fatal(err)
				}
				makeTaskRunCancellationQueueLegacy(t, srv, run.SessionID)
			}
			admission, err := srv.nextSessionRunner(map[string]any{"sessionId": run.SessionID, "runnerId": "old-public", "limit": 20})
			if err != nil {
				t.Fatal(err)
			}
			claim := sessionstore.RunnerClaimFromSession(admission["session"].(sessionstore.Session))
			replacement := startTaskRunCancellationFixture(t, srv, run.RunID)
			before, _, _ := srv.taskRunStore.Get(run.RunID)
			finished, err := srv.finishSessionRunner(map[string]any{"sessionId": run.SessionID, "runnerId": claim.RunnerID, "runnerAttempt": claim.Attempt, "claimToken": claim.ClaimToken, "status": "completed", "clientMessageId": "old-public-finish"})
			if err != nil {
				t.Fatal(err)
			}
			if stringValue(finished["event"].(*eventjournal.Entry).Message["taskRunInstanceId"]) == taskruns.ExecutionInstance(replacement) {
				t.Fatal("old finish used current graph instead of captured receipt")
			}
			after, err := srv.reconcileTaskRun(run.RunID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("old receipt settled/revised replacement: before=%#v after=%#v err=%v", before, after, err)
			}
		})
	}
}

func TestTaskRunCancellationEarlyParentCancelAndNoModelRecovery(t *testing.T) {
	for _, mode := range []string{"parent_cancel", "plain_parent_cancel", "before_native_claim", "no_model"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int64
			srv := newTaskRunCancellationServer(t, taskRunCancelTransport(func(*http.Request) (*http.Response, error) {
				requests.Add(1)
				return nil, errors.New("unexpected provider")
			}))
			run := startTaskRunCancellationFixture(t, srv, "")
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: "early-controlled", Endpoint: "http://controlled.invalid/native", Model: "controlled", DisableSkillDiscovery: true, DisableMCPDiscovery: true, MaxAttempts: 1}
			if mode == "parent_cancel" {
				cancel(ErrGenerationStopped)
			}
			if mode == "plain_parent_cancel" {
				cancel(context.Canceled)
			}
			if mode == "before_native_claim" {
				cancelTaskRunFixture(t, srv, run)
			}
			if mode == "no_model" {
				options.Endpoint = ""
				options.Model = ""
				options.RequireSavedModel = true
			}
			result, err := srv.RunSessionRunnerChatOnce(ctx, options)
			if err != nil || !result.Claimed || requests.Load() != 0 {
				t.Fatalf("early result=%+v requests=%d err=%v", result, requests.Load(), err)
			}
			if mode == "no_model" {
				if result.Status != "interrupted" || !result.AwaitingModelSelection {
					t.Fatalf("setup failure lost recovery: %+v", result)
				}
				_, claimed, err := srv.sessionStore.ClaimNextRunner("recovered-setup", time.Minute)
				if err != nil || !claimed {
					t.Fatalf("setup failure no longer schedulable: claimed=%t err=%v", claimed, err)
				}
			} else {
				if result.Status != "cancelled" {
					t.Fatalf("early cancel=%+v", result)
				}
				finish := taskRunFinishEntry(t, srv, result)
				if stringValue(finish.Message["taskRunInstanceId"]) != taskruns.ExecutionInstance(run) {
					t.Fatalf("early parent cancel guessed/lost admission: %#v", finish)
				}
				graph, err := srv.reconcileTaskRun(run.RunID)
				if err != nil || graph.Status != "cancelled" {
					t.Fatalf("early cancel graph=%#v err=%v", graph, err)
				}
			}
		})
	}
}
