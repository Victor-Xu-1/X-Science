package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// The provider is local, but approval, kernel execution, durable receipts and
// recovery use production paths. The second proposal has valid JSON/schema but
// fails the actual Python compiler before execution; no receipt is invented.
func TestTranscriptRunnerApprovalResumeRetainsRejectedKernelSuccessor(t *testing.T) {
	manager := newServerKernelTestManager(t)
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "project-rejected-successor", "frame-rejected-successor")
	if _, err := db.Exec(`UPDATE frames SET agent_name='OPERON' WHERE id='frame-rejected-successor'`); err != nil {
		t.Fatal(err)
	}
	server := newV11TestServer(t, Options{
		Workspace: store, Transcript: repo, KernelManager: manager, FileRoot: t.TempDir(),
		CondaHome: t.TempDir(), CondaEnvsPath: t.TempDir(),
	})
	t.Cleanup(func() { closeTestServer(t, server) })
	if err := disableBundledMCPForLocalFixture(t.Context(), server, "local"); err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccessContext(context.Background(), "frame-rejected-successor")
	if err != nil || !found {
		t.Fatalf("access=%#v found=%t err=%v", access, found, err)
	}
	projectPath, err := server.defaultAgentKernelTaskWorkspace(access.Frame.ProjectID, access.Frame.RootFrameID, access.RootFrameIncarnationID)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			calls := []any{}
			for _, call := range []struct{ id, code string }{
				{"approved-first", "open('approved-count.txt', 'a').write('x'); print(17)"},
				{"rejected-second", "if True print(2)"},
			} {
				arguments, _ := json.Marshal(map[string]any{"code": call.code, "environment": "python", "human_description": "Check the isolated local calculation"})
				calls = append(calls, map[string]any{"id": call.id, "type": "function", "function": map[string]any{"name": "python", "arguments": string(arguments)}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": calls}}}})
			return
		}
		if !chatRequestHasToolResult(request, "approved-first", "17") ||
			!chatRequestHasToolResult(request, "rejected-second", `"executed":false`) ||
			!chatRequestHasToolResult(request, "rejected-second", "python_syntax_preflight_required") {
			t.Error("resumed provider did not receive real first result and exact second rejection")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "The approved calculation printed 17. The other proposal was not executed because its Python syntax was invalid."}}}})
	}))
	defer provider.Close()
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: "frame-rejected-successor", MessageUUID: "rejected-successor-message", ClientMessageID: "rejected-successor-input",
		Text: "Perform the isolated local calculation and report its printed result. No artifact publication is requested.",
	}); err != nil {
		t.Fatal(err)
	}
	seedAnsweredTaskIntake(t, server, "local", "frame-rejected-successor")
	result, err := runTranscriptRunnerWithLocalExecApproval(t, server, "local", SessionRunnerChatOptions{
		SessionID: "frame-rejected-successor", RunnerID: "rejected-successor-runner",
		Endpoint: provider.URL + "/v1/chat/completions", APIKey: "test-key", Model: "test-model",
		LeaseTTL: time.Minute, ReplayLimit: 100, OutputLimitBytes: 1 << 20, DisableSkillDiscovery: true,
	}, func() {
		if _, err := os.Stat(filepath.Join(projectPath, "approved-count.txt")); !os.IsNotExist(err) {
			t.Fatalf("unapproved call executed: %v", err)
		}
	})
	if err != nil || result.Status != "completed" || requests.Load() != 2 {
		t.Fatalf("result=%#v requests=%d err=%v", result, requests.Load(), err)
	}
	bytes, err := os.ReadFile(filepath.Join(projectPath, "approved-count.txt"))
	if err != nil || string(bytes) != "x" {
		t.Fatalf("approved execution was not exactly once: %q err=%v", bytes, err)
	}
	records, err := store.ListExecutionLog("frame-rejected-successor", "")
	if err != nil || len(records) != 1 || records[0].ExitStatus != "ok" {
		t.Fatalf("actual execution records=%#v err=%v", records, err)
	}
	var operations, starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM kernel_local_operations WHERE tool_call_id='rejected-second'`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_tool_call_items WHERE tool_call_id='rejected-second' AND (started_event_id IS NOT NULL OR waiting_event_id IS NOT NULL)`).Scan(&starts); err != nil {
		t.Fatal(err)
	}
	if operations != 0 || starts != 0 {
		t.Fatalf("rejected proposal obtained execution authority: operations=%d starts=%d", operations, starts)
	}
	assertKernelApprovalEventCounts(t, store, "frame-rejected-successor", 1, 1, 1)
}

func TestRunnerLegacyRejectedOrphanReplaysNonexecutionWithoutReopeningBatch(t *testing.T) {
	fixture := newCorrectionRouteFixture(t)
	run := fixture.run
	call := agentruntime.ToolCall{
		ID: "legacy-rejected", Name: "repl", RejectedBeforeExecution: true,
		Arguments: json.RawMessage(`{"code":"print(1)","human_description":"Legacy rejected proposal"}`),
	}
	options := SessionRunnerChatOptions{RunnerID: run.Transcript.Claim.RunnerID}
	if err := fixture.server.checkpointChatModelToolCalls(options, run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := fixture.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: run.Transcript.Claim, ClientMessageID: "legacy-fatal", Status: "failed", PayloadJSON: []byte(`{"status":"failed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	batchID := run.ToolBatchIDs[call.ID]
	if recovered, err := fixture.store.ReconcileTerminalFrameToolCallBatch(context.Background(), fixture.stream.OwnerID, batchID); err != nil || !recovered {
		t.Fatalf("recovered=%t err=%v", recovered, err)
	}
	entries, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := sessionEntriesToProviderMessages("system", entries)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		if message.Role == "tool" && message.ToolCallID == call.ID {
			found = strings.Contains(message.Content, `"executed":false`) &&
				strings.Contains(message.Content, "original diagnostic was not retained")
		}
	}
	if !found {
		t.Fatal("legacy rejection was replayed as execution uncertainty")
	}
	batch, exists, err := fixture.store.GetToolCallBatch(context.Background(), fixture.stream.OwnerID, batchID)
	if err != nil || !exists || batch.State != workspace.ToolCallBatchStateOutcomeUnknown {
		t.Fatalf("read-only replay changed original audit: batch=%#v err=%v", batch, err)
	}
}
