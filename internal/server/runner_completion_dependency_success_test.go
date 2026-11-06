package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
)

func TestRunnerInvalidInferredReferenceDoesNotAbortCompletedReply(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.server.fileRoot, "regular.dat"), []byte("synthetic data"), 0o600); err != nil {
		t.Fatal(err)
	}
	authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority, ResponseLanguage: "en"}
	const answer = "An illustrative data label is `regular.dat/annotation.txt`."
	var calls atomic.Int64
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer}}}})
	}))
	defer model.Close()
	session := sessionstore.Session{ID: run.SessionID, WorkDir: fixture.projectPath, Project: &sessionstore.Project{ID: fixture.stream.ProjectID}}
	entries := []eventjournal.Entry{{SessionID: session.ID, EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": "Explain an illustrative label in plain text. No files or tools are required."}}}
	got, err := fixture.server.runSessionRunnerChat(t.Context(), SessionRunnerChatOptions{
		RunnerID: fixture.claim.RunnerID, Endpoint: model.URL + "/v1/chat/completions", Model: "synthetic-final",
		RequestTimeout: 5 * time.Second, MaxAttempts: 1, ReplayLimit: 20, OutputLimitBytes: 64 * 1024,
		DisableMCPDiscovery: true, DisableSkillDiscovery: true,
	}, session, entries, run)
	if err != nil || got != answer || calls.Load() != 1 {
		t.Fatalf("inferred non-path prevented a completed reply: error=%v bytes=%d calls=%d", err, len(got), calls.Load())
	}
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	if err := fixture.server.settleSessionRunnerChatOutcomeWithCleanup(t.Context(), SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, result,
		&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, session, nil, got, nil, "",
		func(context.Context, string, *transcriptRunnerAuthority) error { return nil }); err != nil {
		t.Fatal(err)
	}
	terminal, err := fixture.repo.GetTerminalProjection(t.Context(), fixture.stream.OwnerID, fixture.stream.UID, result.FinishEventID)
	if err != nil || result.Status != "completed" || terminal.TerminalStatus != "completed" || terminal.RetainsFinalResponse || terminal.UnvalidatedFinalResponse {
		t.Fatalf("successful reply was converted into a retained failure: result=%#v terminal=%#v err=%v", result, terminal, err)
	}
}
