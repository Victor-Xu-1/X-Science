package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	taskstore "x-science/internal/persistence/tasks"
	"x-science/internal/tools/shellops"
)

func TestTaskOutputCleanupPendingDoesNotProveOngoingWork(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{
		SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt),
		Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim},
	}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	srv := fixture.server
	owner := srv.currentBackgroundShellOwner(ctx)
	if owner == nil {
		t.Fatal("missing live runner owner")
	}
	task, err := srv.taskStore.CreateWithOptions(taskstore.CreateOptions{
		Subject: "background shell", Status: "running", Metadata: map[string]any{"task_type": "shell_command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Model the ownership retained after a failed native close. The process
	// pointer remains attached, but settlement cannot advance on its own.
	entry := srv.reserveBackgroundShell(task.ID, owner)
	defer func() {
		srv.backgroundShellMu.Lock()
		defer srv.backgroundShellMu.Unlock()
		delete(srv.backgroundShells, task.ID)
	}()
	srv.attachBackgroundShell(task.ID, entry, &shellops.RunningCommand{})
	gateway := serverAgentRuntimeToolGateway{
		server: srv, kernel: fixture.identity, sessionID: run.SessionID,
		taskRun: run, allowedTools: []string{"TaskOutput"}, suppressHooks: true,
	}
	wait := func(id string, timeout time.Duration) agentruntime.ToolResult {
		t.Helper()
		arguments, err := json.Marshal(map[string]any{"task_id": task.ID, "block": true, "timeout": timeout.Milliseconds()})
		if err != nil {
			t.Fatal(err)
		}
		result, err := gateway.Execute(ctx, agentruntime.ToolCall{ID: id, Name: "TaskOutput", Arguments: arguments})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	active := wait("active-shell", 10*time.Millisecond)
	if payload := mapValue(mapValue(active.Value)["result"]); payload["retrieval_status"] != "timeout" || !active.PendingWorkWait {
		t.Fatalf("active owned Shell wait lost its timeout or existing proof: %#v", active)
	}
	srv.settleBackgroundShell(task.ID, entry, backgroundShellSettlement{}, errors.New("native close refused"))
	const cleanupTimeout = 4 * time.Second
	started := time.Now()
	result := wait("pending-native-cleanup", cleanupTimeout)
	payload := mapValue(mapValue(result.Value)["result"])
	taskPayload := mapValue(payload["task"])
	if payload["retrieval_status"] != "not_ready" || taskPayload["cleanupPending"] != true || taskPayload["error"] != "native close refused" {
		t.Fatalf("known cleanup did not return the existing not-ready result: %#v", result.Value)
	}
	if elapsed := time.Since(started); elapsed >= cleanupTimeout/2 {
		t.Fatalf("known cleanup unnecessarily waited %s", elapsed)
	}
	if result.PendingWorkWait {
		t.Fatal("retained cleanup was certified as ongoing work")
	}
}
