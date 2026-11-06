package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerArtifactEvidenceFailureRetainsSealedModelCandidate(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority, ResponseLanguage: "en"}
	// Exercise a genuine hard storage-access error, independent of the invalid
	// inferred-path regression. Restore mode before TempDir cleanup on POSIX.
	directory := filepath.Join(fixture.server.fileRoot, "private-evidence")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "snapshot.dat")
	if err := os.WriteFile(path, []byte("synthetic evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
	if _, err := os.Stat(path); !errors.Is(err, os.ErrPermission) {
		t.Skip("this OS/user does not enforce the isolated POSIX directory permission fixture")
	}
	answer := "Synthetic values are recorded in `private-evidence/snapshot.dat`."
	var calls atomic.Int64
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer}}}})
	}))
	defer model.Close()
	session := sessionstore.Session{ID: run.SessionID, WorkDir: fixture.projectPath, Project: &sessionstore.Project{ID: fixture.stream.ProjectID}}
	entries := []eventjournal.Entry{{SessionID: session.ID, EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": "Explain a synthetic calculation in English."}}}
	got, cause := fixture.server.runSessionRunnerChat(t.Context(), SessionRunnerChatOptions{
		RunnerID: fixture.claim.RunnerID, Endpoint: model.URL + "/v1/chat/completions", Model: "synthetic-final",
		RequestTimeout: 5 * time.Second, MaxAttempts: 1, ReplayLimit: 20, OutputLimitBytes: 64 * 1024,
		DisableMCPDiscovery: true, DisableSkillDiscovery: true,
	}, session, entries, run)
	var completion *sessionRunnerCompletionFailure
	var pathError *os.PathError
	if calls.Load() != 1 || !errors.As(cause, &completion) || completion.stage != completionArtifactEvidence || !errors.As(cause, &pathError) || !errors.Is(cause, os.ErrPermission) {
		t.Fatalf("fixture missed sealed post-provider evidence failure: calls=%d stage=%#v cause=%T", calls.Load(), completion, cause)
	}
	if got != answer {
		t.Fatalf("sealed final candidate was lost: got %d bytes, want %d", len(got), len(answer))
	}
	assertArtifactEvidenceCandidateSettlement(t, fixture, authority, run, got, cause, nil, "")
}

func TestRunnerArtifactEvidenceHardFailureRetainsDraftWithoutCompleting(t *testing.T) {
	for _, test := range []struct {
		name, language string
		cleanupError   error
	}{{name: "en", language: "en"}, {name: "zh", language: "zh"}, {name: "cleanup also failed", language: "en", cleanupError: errors.New("PRIVATE_CLEANUP_SENTINEL")}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentSaveArtifactsFixture(t)
			authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
			run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority, ResponseLanguage: test.language}
			if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
				t.Fatal(err)
			}
			cause := &sessionRunnerCompletionFailure{stage: completionArtifactEvidence, err: &os.PathError{Op: "open", Path: "PRIVATE_PATH_SENTINEL", Err: syscall.EACCES}}
			assertArtifactEvidenceCandidateSettlement(t, fixture, authority, run, "The synthetic final draft remains available.", cause, test.cleanupError, "")
		})
	}
}

func TestRunnerArtifactEvidenceStopHookFailureRetainsOnlyQualifiedDraft(t *testing.T) {
	for _, qualified := range []bool{true, false} {
		name := "without evidence candidate"
		if qualified {
			name = "sealed evidence candidate"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newAgentSaveArtifactsFixture(t)
			authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
			run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority, ResponseLanguage: "en"}
			if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
				t.Fatal(err)
			}
			var hookCalls atomic.Int64
			hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				hookCalls.Add(1)
				var input map[string]any
				if json.NewDecoder(request.Body).Decode(&input) != nil || input["hook_event_name"] != "Stop" || input["status"] != "failed" {
					t.Error("hook did not observe the original failed settlement")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"decision":"block","reason":"PRIVATE_STOP_HOOK_SENTINEL"}`))
			}))
			defer hook.Close()
			if _, err := fixture.server.settingsStore.Set("hooks", map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "http", "url": hook.URL, "timeout": 5}}}}}); err != nil {
				t.Fatal(err)
			}
			var cause error = errors.New("synthetic unrelated execution failure")
			if qualified {
				cause = &sessionRunnerCompletionFailure{stage: completionArtifactEvidence, err: &os.PathError{Op: "open", Path: "PRIVATE_PATH_SENTINEL", Err: syscall.EACCES}}
				assertArtifactEvidenceCandidateSettlement(t, fixture, authority, run, "The synthetic final draft remains available.", cause, nil, "runner_stop_hook_failed")
			} else {
				result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
				if err := fixture.server.settleSessionRunnerChatOutcomeWithCleanup(t.Context(), SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, result,
					&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil, "", cause, "",
					func(context.Context, string, *transcriptRunnerAuthority) error { return nil }); err != nil {
					t.Fatal(err)
				}
				terminal, err := fixture.repo.GetTerminalProjection(t.Context(), fixture.stream.OwnerID, fixture.stream.UID, result.FinishEventID)
				if err != nil || terminal.ReasonCode != "runner_stop_hook_failed" || terminal.RetainsFinalResponse || result.Status != "failed" {
					t.Fatalf("unqualified hook failure changed: terminal=%#v err=%v", terminal, err)
				}
			}
			if hookCalls.Load() != 1 {
				t.Fatalf("Stop hook calls=%d want one", hookCalls.Load())
			}
		})
	}
}

func assertArtifactEvidenceCandidateSettlement(t *testing.T, fixture *agentSaveArtifactsFixture, authority *transcriptRunnerAuthority, run *sessionRunnerChatRun, answer string, cause, cleanupError error, expectedReason string) {
	t.Helper()
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	if err := fixture.server.settleSessionRunnerChatOutcomeWithCleanup(t.Context(), SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, result,
		&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil, answer, cause, "",
		func(context.Context, string, *transcriptRunnerAuthority) error { return cleanupError }); err != nil {
		t.Fatal(err)
	}
	terminal, err := fixture.repo.GetTerminalProjection(t.Context(), fixture.stream.OwnerID, fixture.stream.UID, result.FinishEventID)
	reason := sessionRunnerExecutionFailedReasonCode
	if cleanupError != nil {
		reason = transcriptstore.RunnerCleanupFailedReason
	}
	if expectedReason != "" {
		reason = expectedReason
	}
	if err != nil || result.Status != "failed" || terminal.ReasonCode != reason || !terminal.RetainsFinalResponse || !terminal.UnvalidatedFinalResponse || result.InterruptionAutoResume {
		t.Fatalf("evidence failure changed settlement authority: result=%#v terminal=%#v err=%v", result, terminal, err)
	}
	assistant, found, err := fixture.repo.GetEventByClientMessageID(t.Context(), fixture.stream.UID, fixture.stream.OwnerID, transcriptRunnerClientMessageID(fixture.claim, "assistant-message"))
	var payload map[string]any
	if err != nil || !found || json.Unmarshal(assistant.PayloadJSON, &payload) != nil || payload["text"] != answer {
		t.Fatalf("exact durable draft missing: found=%t err=%v", found, err)
	}
	if terminal.Detail != publicSessionRunnerRetainedEvidenceFailureMessage(run.ResponseLanguage, reason) {
		t.Fatalf("durable draft notice lost language/failure distinction: %q", terminal.Detail)
	}
	if _, err := fixture.repo.GetTerminalProjection(t.Context(), "foreign-owner", fixture.stream.UID, result.FinishEventID); !errors.Is(err, transcriptstore.ErrOwnerMismatch) {
		t.Fatalf("retained draft relaxed owner fence: %v", err)
	}
	if err := fixture.server.runTranscriptWebReadModelCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	history := compatJSONRequest(t, fixture.server.Handler(), http.MethodGet, "/api/conversations/"+fixture.stream.FrameID+"/messages?limit=10", fixture.stream.OwnerID, nil, http.StatusOK)
	raw, _ := json.Marshal(history)
	if strings.Contains(string(raw), "PRIVATE_PATH_SENTINEL") || strings.Contains(string(raw), "PRIVATE_CLEANUP_SENTINEL") || strings.Contains(string(raw), "PRIVATE_STOP_HOOK_SENTINEL") || !strings.Contains(string(raw), "final draft") && !strings.Contains(string(raw), "最终草稿") {
		t.Fatalf("history lacks honest draft notice or leaks error: %s", raw)
	}
	assertArtifactEvidenceRetainedReplay(t, fixture, answer, reason)
}

func assertArtifactEvidenceRetainedReplay(t *testing.T, fixture *agentSaveArtifactsFixture, answer, reason string) {
	t.Helper()
	full, found, err := fixture.server.loadTranscriptWebHistory(t.Context(), fixture.stream.OwnerID, fixture.stream.SessionID)
	if err != nil || !found {
		t.Fatalf("full history found=%t err=%v", found, err)
	}
	var draft, notice bool
	for _, message := range full {
		if webString(mapValue(message["content"])["content"]) == answer {
			draft = message["status"] == "finish" && message["terminal_status"] == nil
		}
		if message["terminal_final_response_unvalidated"] == true {
			notice = message["terminal_status"] == "failed" && message["terminal_reason_code"] == reason
		}
	}
	if !draft || !notice {
		t.Fatalf("full history draft=%t separate failed notice=%t", draft, notice)
	}
	if err := fixture.server.drainTranscriptWebDeliveries(t.Context()); err != nil {
		t.Fatal(err)
	}
	var liveDraft, liveFailure bool
	for _, event := range transcriptWebEvents(t, fixture.store, fixture.stream.OwnerID) {
		if event.Type != "message.stream" {
			continue
		}
		if event.Payload["stream_type"] == "content" && event.Payload["data"] == answer {
			liveDraft = true
		}
		if event.Payload["stream_type"] == "error" && event.Payload["terminal_final_response_unvalidated"] == true && event.Payload["terminal_status"] == "failed" {
			liveFailure = true
		}
	}
	if !liveDraft || !liveFailure {
		t.Fatalf("live retained draft=%t failed notice=%t", liveDraft, liveFailure)
	}
}
