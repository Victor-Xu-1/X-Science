package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryconfig"
	sessionstore "x-science/internal/persistence/sessions"
	settingsstore "x-science/internal/persistence/settings"
	workspace "x-science/internal/persistence/workspace"
)

func TestMemoryExtractionCommittedCompletionSurvivesRestartBeforeScheduling(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := t.Context()
	f.server.memoryConfig = memoryconfig.Default()
	f.server.memoryConfig.Enabled = true
	f.server.memoryConfig.ExtractEnabled = true
	f.server.memoryConfig.ExtractEveryN = 1
	f.server.memoryConfig.PIClassifierEnabled = false
	f.server.settingsStore = settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
	if err := f.store.SetMemoryEnabled(ctx, f.stream.OwnerID, true); err != nil {
		t.Fatal(err)
	}
	if f.server.memoryConfig.ExtractMode != "forked" {
		t.Fatal("fixture must exercise the normal default forked mode")
	}
	if enabled, err := f.server.memoryAutoExtractionEnabled(f.stream.OwnerID); err != nil || !enabled {
		t.Fatalf("fixture automatic extraction enabled=%t err=%v", enabled, err)
	}
	f.server.memoryExtraction.rememberLastRequest(f.stream.SessionID, f.claim.Attempt, agentruntime.ModelRequest{
		Messages: []agentruntime.Message{{Role: "user", Content: "ORIGINAL_FORKED_REQUEST_VIEW: keep explicit random seeds in reproducible reports."}},
		Headers:  map[string]string{"synthetic-private-header": "TRANSIENT_HEADER_MUST_NOT_BE_PERSISTED"},
		Metadata: map[string]any{"synthetic_live_metadata": "TRANSIENT_METADATA_MUST_NOT_BE_PERSISTED"},
	})
	authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), Transcript: authority}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	// Suppress only the old in-process post-commit dispatch. This is a
	// controlled producer/reopen regression, not a simulated process kill.
	// The real settlement must retain its nonempty root-assistant eligibility.
	f.server.memoryExtraction.tasks = nil
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	err := f.server.settleSessionRunnerChatOutcome(ctx, SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}, result,
		&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil,
		"Recorded the reproducible reporting convention.", nil, "")
	if err != nil || result.Status != "completed" || result.FinishEventID <= 0 {
		t.Fatalf("canonical root-chat settlement=%+v err=%v", result, err)
	}
	if _, err := f.repo.ReadCompletedMemorySource(ctx, f.stream.UID, f.stream.OwnerID, result.FinishEventID); err != nil {
		t.Fatalf("fixture did not commit a valid completed source: %v", err)
	}
	// Canonical settlement committed while the RAM-only dispatch was
	// unavailable. Reopening must not inherit the old runtime, its goroutines
	// or its request registry to recover the source-bound durable work.
	reopened, err := workspace.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	repo, err := reopened.TranscriptRepository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(Options{Workspace: reopened, Transcript: repo, FileRoot: f.server.fileRoot})
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = restarted.Close(closeCtx)
	})
	if len(restarted.memoryExtraction.sessions) != 0 {
		t.Fatal("restarted runtime inherited the old process-local registry")
	}
	if _, err := repo.ReadCompletedMemorySource(ctx, f.stream.UID, f.stream.OwnerID, result.FinishEventID); err != nil {
		t.Fatalf("completed source did not survive reopening: %v", err)
	}
	events, err := reopened.ClaimOutbox(ctx, workspace.ClaimOutboxInput{
		WorkerID: "memory-restart-fixture", Topics: []string{"memory.extraction"}, Limit: 1, Lease: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("completed canonical input lost its durable Memory dispatch before scheduling: claimed=%d", len(events))
	}
	event := events[0]
	if event.Topic != "memory.extraction" || event.Type != "memory.extraction.completed.v1" ||
		len(event.IdempotencyKey) != 64 || event.ClaimToken == "" || event.LeaseExpiresAt == nil || !json.Valid(event.Payload) {
		t.Fatalf("Memory restart did not retain the existing outbox identity/lease contract: %+v", event)
	}
	if !strings.Contains(string(event.Payload), "ORIGINAL_FORKED_REQUEST_VIEW") {
		t.Fatal("durable default forked dispatch lost its original source-bound request view")
	}
	for _, forbidden := range []string{"TRANSIENT_HEADER_MUST_NOT_BE_PERSISTED", "TRANSIENT_METADATA_MUST_NOT_BE_PERSISTED"} {
		if strings.Contains(string(event.Payload), forbidden) {
			t.Fatalf("durable request view serialized a transient/private request field: %s", forbidden)
		}
	}
	if cursor, err := reopened.LastExtractMessageIndex(ctx, f.stream.FrameID); err != nil || cursor != 0 {
		t.Fatalf("enqueue/restart itself mutated the extraction cursor: cursor=%d err=%v", cursor, err)
	}
}
