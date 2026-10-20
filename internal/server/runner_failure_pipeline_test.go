package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerPreparationFailureRealPathAndPublicProjection(t *testing.T) {
	for _, tc := range []struct {
		name, reason, cause string
		history             bool
	}{
		{"history", "runner_history_preparation_failed", "session not found", true},
		{"other", "runner_preparation_failed", "requires at least one user message", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int64
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"unexpected model call"}}]}`))
			}))
			defer model.Close()
			server := New(Options{FileRoot: t.TempDir()})
			if _, err := server.settingsStore.Set(configStoreKey("autoCompactEnabled"), tc.history); err != nil {
				t.Fatal(err)
			}
			if _, err := server.settingsStore.Set(configStoreKey("autoCompactTokenThreshold"), float64(1)); err != nil {
				t.Fatal(err)
			}
			session := sessionstore.Session{ID: "unpersisted-PRIVATE_PREPARATION_SECRET", WorkDir: t.TempDir()}
			var entries []eventjournal.Entry
			if tc.history {
				entries = []eventjournal.Entry{{SessionID: session.ID, EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": "A bounded synthetic preparation request."}}}
			}
			run := &sessionRunnerChatRun{SessionID: session.ID, Attempt: 1}
			_, cause := server.runSessionRunnerChat(context.Background(), SessionRunnerChatOptions{
				RunnerID: "preparation-classification", Endpoint: model.URL, Model: "synthetic-unused",
				RequestTimeout: time.Second, PreparationTimeout: 5 * time.Second, MaxAttempts: 1,
				ReplayLimit: 20, OutputLimitBytes: 64 * 1024, DisableMCPDiscovery: true, DisableSkillDiscovery: true,
			}, session, entries, run)
			if cause == nil || !strings.Contains(cause.Error(), tc.cause) {
				t.Fatalf("fixture did not reach its real preparation boundary: %v", cause)
			}
			if requests.Load() != 0 {
				t.Fatalf("preparation failure called the model %d times", requests.Load())
			}
			if reason := sessionRunnerErrorReasonCode(cause); reason != tc.reason {
				t.Errorf("preparation reason=%q want=%q", reason, tc.reason)
			}
			fixture := newAgentSaveArtifactsFixture(t)
			authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
			settlingRun := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority, ResponseLanguage: "en"}
			if err := ensureSessionRunnerExecutionPhaseMachine(settlingRun); err != nil {
				t.Fatal(err)
			}
			result := &SessionRunnerCycleResult{SessionID: settlingRun.SessionID, Attempt: settlingRun.Attempt}
			if err := fixture.server.settleSessionRunnerChatOutcome(context.Background(), SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, result, &activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, settlingRun, sessionstore.Session{ID: settlingRun.SessionID}, nil, "", cause, ""); err != nil {
				t.Fatal(err)
			}
			projection, err := fixture.repo.GetTerminalProjection(context.Background(), fixture.stream.OwnerID, fixture.stream.UID, result.FinishEventID)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != "failed" || result.InterruptionAutoResume || projection.ReasonCode != tc.reason {
				t.Errorf("preparation changed settlement/recovery or lost reason: result=%#v projection=%#v", result, projection)
			}
			frame := compatJSONRequest(t, fixture.server.Handler(), http.MethodGet, "/api/frames/"+fixture.stream.FrameID, fixture.stream.OwnerID, nil, http.StatusOK)
			if frame["runtime_failure_reason"] != tc.reason {
				t.Errorf("frame reason=%#v want=%q", frame["runtime_failure_reason"], tc.reason)
			}
			history := compatJSONRequest(t, fixture.server.Handler(), http.MethodGet, "/api/conversations/"+fixture.stream.FrameID+"/messages?limit=10", fixture.stream.OwnerID, nil, http.StatusOK)
			raw, err := json.Marshal(history)
			if err != nil || !strings.Contains(string(raw), `"terminal_reason_code":"`+tc.reason+`"`) || strings.Contains(string(raw), "PRIVATE_PREPARATION_SECRET") {
				t.Errorf("history lost preparation reason or leaked cause: %s err=%v", raw, err)
			}
			frameContext, found, err := fixture.store.GetFrameRealtimeContext(fixture.stream.FrameID)
			if err != nil || !found {
				t.Fatalf("frame context found=%t err=%v", found, err)
			}
			if err := fixture.server.publishTranscriptTerminal(context.Background(), frameContext, "preparation-failure-test", projection, "preparation-failure-message"); err != nil {
				t.Fatal(err)
			}
			raw, err = json.Marshal(transcriptWebEvents(t, fixture.store, fixture.stream.OwnerID))
			if err != nil || !strings.Contains(string(raw), `"reason_code":"`+tc.reason+`"`) || strings.Contains(string(raw), "PRIVATE_PREPARATION_SECRET") {
				t.Errorf("live preparation reason missing/leaked: %s err=%v", raw, err)
			}
		})
	}
}

func TestRunnerPreparationFailureFormatterCodes(t *testing.T) {
	for _, tc := range []struct{ reason, language, expected string }{
		{"runner_history_preparation_failed", "en", "Conversation history could not be prepared for this run. Existing progress and results were preserved; inspect the run details before continuing."},
		{"runner_history_preparation_failed", "zh", "本次运行未能完成对话历史准备。已有进度和结果已保留；请检查运行详情后再继续。"},
		{"runner_preparation_failed", "en", "Task preparation did not finish. Existing progress and results were preserved; inspect the run details before continuing."},
		{"runner_preparation_failed", "zh", "任务准备未完成。已有进度和结果已保留；请检查运行详情后再继续。"},
	} {
		t.Run(tc.reason+"/"+tc.language, func(t *testing.T) {
			raw := `PRIVATE_PREPARATION_SECRET /private/path?token=secret {"credential":"do-not-expose"} history_compaction`
			if got := publicSessionRunnerFailureMessage(raw, tc.language, tc.reason); got != tc.expected {
				t.Fatalf("safe preparation explanation=%q want=%q", got, tc.expected)
			}
		})
	}
}

func TestRunnerPreparationFailureTypedPriority(t *testing.T) {
	protected := []struct {
		name  string
		cause error
	}{
		{"nil", nil},
		{"cancel", fmt.Errorf("cancel retained: %w", context.Canceled)},
		{"deadline", fmt.Errorf("deadline retained: %w", context.DeadlineExceeded)},
		{"known_correction", sessionRunnerResponseLanguageMismatch{}},
		{"preparation_timeout", sessionRunnerPreparationTimeout{stage: "history_replay", timeout: time.Second, cause: context.DeadlineExceeded}},
		{"provider", errors.New("provider endpoint returned 401: authentication unavailable")},
		{"transport_timeout", errors.New("provider stream response headers timeout")},
	}
	for _, tc := range protected {
		t.Run(tc.name, func(t *testing.T) {
			before := sessionRunnerErrorReasonCode(tc.cause)
			got := classifySessionRunnerPreparationFailure("history_compaction", tc.cause)
			if got != tc.cause || sessionRunnerErrorReasonCode(got) != before {
				t.Fatalf("preparation changed existing priority: before=%q cause=%v got=%v", before, tc.cause, got)
			}
		})
	}
	for _, stage := range []string{"history_replay", "history_compaction", "context_replay", "tool_discovery", "unknown", "untrusted history_replay"} {
		t.Run(stage, func(t *testing.T) {
			sentinel := errors.New("private diagnostic")
			cause := &os.PathError{Op: "read", Path: "/private/PRIVATE_PREPARATION_SECRET", Err: sentinel}
			got := classifySessionRunnerPreparationFailure(stage, cause)
			var typed *os.PathError
			var correction sessionRunnerBoundedCorrection
			if !errors.Is(got, sentinel) || !errors.As(got, &typed) || typed != cause || got.Error() != cause.Error() || errors.As(got, &correction) {
				t.Fatalf("preparation wrapper lost cause or granted correction: %v", got)
			}
			want := "runner_preparation_failed"
			if stage == "history_replay" || stage == "history_compaction" || stage == "context_replay" {
				want = "runner_history_preparation_failed"
			}
			if sessionRunnerErrorReasonCode(got) != want || classifySessionRunnerPreparationFailure(stage, got) != got || runnerInterruptionMayContinueSameTask(want) || runnerInterruptionAutoResume(want) {
				t.Fatalf("wrong trusted reason, repeated wrapper or new recovery grant: %v", got)
			}
		})
	}
	if got := sessionRunnerErrorReasonCode(errors.New("history_replay runner_history_preparation_failed")); got != "runner_execution_failed" {
		t.Fatalf("raw text fabricated preparation identity: %q", got)
	}
}

func TestRunnerSettlementPersistsSafeFailureReasonThroughFrameAndHistory(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority, ResponseLanguage: "zh"}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	err := fixture.server.settleSessionRunnerChatOutcome(context.Background(), SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, result, &activeSessionRun{},
		sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil, "", errors.New("internal computation failure at /private/path?token=SECRET"), "")
	if err != nil {
		t.Fatal(err)
	}
	projection, err := fixture.repo.GetTerminalProjection(context.Background(), fixture.stream.OwnerID, fixture.stream.UID, result.FinishEventID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.ReasonCode != "runner_execution_failed" || strings.Contains(projection.Detail, "SECRET") || strings.Contains(projection.Detail, "/private") {
		t.Fatalf("lost reason or leaked detail: %#v", projection)
	}
	frame := compatJSONRequest(t, fixture.server.Handler(), http.MethodGet, "/api/frames/"+fixture.stream.FrameID, fixture.stream.OwnerID, nil, http.StatusOK)
	if frame["runtime_failure_reason"] != "runner_execution_failed" {
		t.Fatalf("frame reason=%#v", frame["runtime_failure_reason"])
	}
	history := compatJSONRequest(t, fixture.server.Handler(), http.MethodGet, "/api/conversations/"+fixture.stream.FrameID+"/messages?limit=10", fixture.stream.OwnerID, nil, http.StatusOK)
	raw, err := json.Marshal(history)
	if err != nil || !strings.Contains(string(raw), "\"terminal_reason_code\":\"runner_execution_failed\"") || strings.Contains(string(raw), "SECRET") {
		t.Fatalf("history lost safe reason: %s err=%v", raw, err)
	}
	compatJSONRequest(t, fixture.server.Handler(), http.MethodGet, "/api/frames/"+fixture.stream.FrameID, "foreign-owner", nil, http.StatusNotFound)
	frameContext, found, err := fixture.store.GetFrameRealtimeContext(fixture.stream.FrameID)
	if err != nil || !found {
		t.Fatalf("frame context: found=%t err=%v", found, err)
	}
	if err := fixture.server.publishTranscriptTerminal(context.Background(), frameContext, "failure-pipeline-test", projection, "failure-pipeline-message"); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(transcriptWebEvents(t, fixture.store, fixture.stream.OwnerID))
	if err != nil || strings.Count(string(raw), "\"reason_code\":\"runner_execution_failed\"") < 2 || !strings.Contains(string(raw), "\"terminal_reason_code\":\"runner_execution_failed\"") || strings.Contains(string(raw), "SECRET") {
		t.Fatalf("live event reasons missing/leaked: %s err=%v", raw, err)
	}
}

func TestPresentationCorrectionRetainsCompletedReceiptAndBoundedRecovery(t *testing.T) {
	for _, failure := range []sessionRunnerBoundedCorrection{sessionRunnerResponseLanguageMismatch{}, sessionRunnerFinalPresentationCorrection{}} {
		t.Run(failure.runnerCorrection().ReasonCode, func(t *testing.T) {
			fixture := newAgentSaveArtifactsFixture(t)
			ctx := context.Background()
			appendRunnerToolCheckpoint(t, fixture.repo, fixture.claim, "verified-download", map[string]any{
				"toolName": "download_public_scientific_file", "toolPhase": "completed", "status": "completed", "toolCallId": "completed-download",
				"toolResult": map[string]any{"ok": true, "download": map[string]any{"filename": "resource.txt", "size_bytes": 128}},
			})
			claim := fixture.claim
			for attempt := 0; attempt < 3; attempt++ {
				authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: claim}
				run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(claim.Attempt), Transcript: authority}
				entries, err := fixture.server.loadTranscriptRunnerReplay(ctx, authority, 20, 20)
				if err != nil {
					t.Fatal(err)
				}
				result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
				var callErr error = failure
				handled, err := fixture.server.handleSessionRunnerChatInterruption(ctx, SessionRunnerChatOptions{RunnerID: claim.RunnerID, SessionID: run.SessionID}, result, &activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, entries, &callErr, nil)
				if err != nil || !handled {
					t.Fatalf("presentation became terminal: handled=%t err=%v", handled, err)
				}
				if !result.InterruptionAutoResume {
					t.Fatalf("retry policy=%#v", result)
				}
				checkpoint, found, err := fixture.repo.LatestResumableCheckpoint(ctx, fixture.stream.UID, fixture.stream.OwnerID)
				if err != nil || !found {
					t.Fatalf("checkpoint found=%t err=%v", found, err)
				}
				replayed, err := fixture.server.loadTranscriptRunnerReplay(ctx, authority, 20, 20)
				if err != nil {
					t.Fatal(err)
				}
				kept := false
				for _, entry := range replayed {
					if eventjournal.Message(entry.Message)["toolCallId"] == "completed-download" {
						kept = true
					}
				}
				if !kept {
					t.Fatal("completed acquisition receipt lost")
				}
				if attempt < 2 {
					next, err := fixture.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: fmt.Sprintf("presentation-recovery-%d", attempt), TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence})
					if err != nil || !next.Claimed {
						t.Fatalf("resume=%#v err=%v", next, err)
					}
					claim = next.Claim
				}
			}
		})
	}
}
