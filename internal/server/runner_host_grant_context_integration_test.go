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
	kernelruntime "x-science/internal/kernel"
)

// Exercise the provider request, not a context helper: a durable discovery is
// historical evidence after the real host-access API changes its snapshot.
func TestRunnerHostGrantContextRefreshesChangedDiscoveryInActualProviderRequest(t *testing.T) {
	// Only permission metadata executes here, never a worker or user code. The
	// existing readiness seam isolates unrelated host confinement availability;
	// the API, discovery, live claim, checkpoints and HTTP provider remain real.
	// This is not evidence of OS confinement or deployed browser acceptance.
	f := newAgentSaveArtifactsFixtureWithKernelManager(t, kernelruntime.NewManager(kernelruntime.Config{}))
	f.server.kernelConfinement = func(bool) kernelruntime.ConfinementEvidence {
		return kernelruntime.ConfinementEvidence{Available: true, Mode: "metadata-only-test-boundary"}
	}
	grantA := filepath.Join(t.TempDir(), "historical-directory")
	grantB := filepath.Join(t.TempDir(), "current-directory")
	for _, path := range []string{grantA, grantB} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	app := f.server.Handler()
	runtimeCompatJSON(t, app, http.MethodPost, "/api/go/preferences/host-grants", f.stream.OwnerID,
		map[string]any{"path": grantA, "mode": "read"}, http.StatusOK)

	const originalCallID = "historical-host-directory-discovery"
	originalResult, originalCheckpoint := seedRunnerHostGrantDiscovery(t, f, originalCallID)
	if !hostGrantTestResultHasPath(originalResult, grantA) || hostGrantTestResultHasPath(originalResult, grantB) {
		t.Fatalf("original permission tool did not discover snapshot A: %s", originalResult)
	}
	finishSkillControlInput(t, f)
	// Queue the ordinary input before changing the grants. The eventual request
	// must use live execution-time authorization, not submission-time state.
	appendSkillControlInput(t, f, "directory-discovery-continuation",
		"Continue by listing the currently authorized local directories. Do not read, write, or execute files.", false)
	runtimeCompatJSON(t, app, http.MethodDelete, "/api/go/preferences/host-grants", f.stream.OwnerID,
		map[string]any{"path": grantA}, http.StatusOK)
	runtimeCompatJSON(t, app, http.MethodPost, "/api/go/preferences/host-grants", f.stream.OwnerID,
		map[string]any{"path": grantB, "mode": "read"}, http.StatusOK)
	run := claimSkillControlInput(t, f)
	entries, err := f.server.loadTranscriptRunnerReplay(context.Background(), run.Transcript, 20, 100)
	if err != nil {
		t.Fatal(err)
	}
	session, err := f.server.loadTranscriptFrameSessionProjection(f.stream)
	if err != nil {
		t.Fatal(err)
	}

	requests := make(chan map[string]any, 4)
	var requestCount atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode actual provider request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- request
		message := map[string]any{"role": "assistant", "content": "The current authorized directory listing has been refreshed. No files were read or changed."}
		if requestCount.Add(1) == 1 {
			message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"id": "refresh-directory-discovery", "type": "function",
				"function": map[string]any{"name": "list_host_grants", "arguments": "{}"},
			}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	}))
	defer provider.Close()
	options := normalizeSessionRunnerChatOptions(SessionRunnerChatOptions{
		RunnerID: f.claim.RunnerID, SessionID: f.stream.SessionID,
		Endpoint: provider.URL + "/v1/chat/completions", Model: "directory-context-protocol",
		AllowedTools: []string{"list_host_grants"}, DisableMCPDiscovery: true, DisableSkillDiscovery: true,
		RequestTimeout: 5 * time.Second, PreparationTimeout: 5 * time.Second,
		MaxToolRounds: 2, MaxAttempts: 1, ReplayLimit: 20, OutputLimitBytes: 64 * 1024,
	})
	result, err := f.server.runSessionRunnerChat(withTranscriptRunnerChatRun(context.Background(), run), options, session, entries, run)
	if err != nil {
		t.Fatal(err)
	}
	if requestCount.Load() != 2 || !strings.Contains(result, "has been refreshed") {
		t.Fatalf("normal runner did not complete discovery: requests=%d result=%q", requestCount.Load(), result)
	}
	first, second := <-requests, <-requests
	firstMessages, _ := first["messages"].([]any)
	secondMessages, _ := second["messages"].([]any)
	if tools, _ := first["tools"].([]any); !hasChatToolNamed(tools, "list_host_grants") {
		t.Fatal("actual provider request did not offer the ordinary directory-discovery tool")
	}
	if replayed := hostGrantTestToolResult(t, firstMessages, originalCallID); replayed != string(originalResult) {
		t.Fatalf("original snapshot A was rewritten in provider history: got=%s want=%s", replayed, originalResult)
	}
	refreshedCallID := requireProviderToolCallID(t, second, "list_host_grants", 1)
	refreshed := []byte(hostGrantTestToolResult(t, secondMessages, refreshedCallID))
	if !hostGrantTestResultHasPath(refreshed, grantB) || hostGrantTestResultHasPath(refreshed, grantA) {
		t.Fatalf("normal gateway did not return live snapshot B: %s", refreshed)
	}
	var durableCheckpoint []byte
	if err := f.db.QueryRow(`SELECT payload_json FROM transcript_events
		WHERE stream_uid=? AND json_extract(payload_json,'$.toolCallId')=?
		AND json_extract(payload_json,'$.toolPhase')='completed'`, f.stream.UID, originalCallID).Scan(&durableCheckpoint); err != nil {
		t.Fatal(err)
	}
	if string(durableCheckpoint) != string(originalCheckpoint) {
		t.Fatalf("authorization change rewrote historical canonical checkpoint: got=%s want=%s", durableCheckpoint, originalCheckpoint)
	}

	// This exact behavior fails on the old engine even though the actual tool,
	// canonical history, API mutation, fresh claim, and provider round all work.
	const notice = "Local directory authorization has changed. Refresh list_host_grants before the next local file operation; previous directory results are historical, not current permission."
	if count := hostGrantTestNoticeCount(firstMessages, notice); count != 1 {
		t.Errorf("changed authorization refresh notice count=%d, want exactly 1 in the actual provider request", count)
	}
	if count := hostGrantTestNoticeCount(secondMessages, notice); count != 0 {
		t.Errorf("successful fresh discovery did not retire the refresh notice: count=%d", count)
	}
	for _, raw := range firstMessages {
		message, _ := raw.(map[string]any)
		content, _ := message["content"].(string)
		if strings.Contains(content, "Local directory authorization has changed.") &&
			(strings.Contains(content, grantA) || strings.Contains(content, grantB)) {
			t.Error("authorization refresh notice disclosed a directory path")
		}
	}
}

func seedRunnerHostGrantDiscovery(t *testing.T, f *agentSaveArtifactsFixture, callID string) ([]byte, []byte) {
	t.Helper()
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	options := SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}
	call := agentruntime.ToolCall{ID: callID, Name: "list_host_grants", Arguments: json.RawMessage(`{}`)}
	if err := f.server.checkpointChatModelToolCallsWithContext(ctx, options, run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
		Type: agentruntime.EventToolStarted, ToolCallID: callID, ToolName: call.Name, Arguments: string(call.Arguments),
	}); err != nil {
		t.Fatal(err)
	}
	// Use the existing permission executor, never a fabricated legacy shape or
	// a new snapshot helper. The fix therefore changes A through its real owner.
	value, err := f.server.executeAgentPermissionTool(ctx, f.identity, call, call.Name, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if agentruntime.ClassifyToolResult(value) != agentruntime.ToolResultSucceeded {
		t.Fatalf("original directory discovery did not succeed: %s", result)
	}
	if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
		Type: agentruntime.EventToolCompleted, ToolCallID: callID, ToolName: call.Name,
		Arguments: string(call.Arguments), Result: string(result),
	}); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := f.db.QueryRow(`SELECT payload_json FROM transcript_events
		WHERE stream_uid=? AND json_extract(payload_json,'$.toolCallId')=?
		AND json_extract(payload_json,'$.toolPhase')='completed'`, f.stream.UID, callID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	return result, payload
}

func hostGrantTestToolResult(t *testing.T, messages []any, callID string) string {
	t.Helper()
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		if message["role"] != "tool" || message["tool_call_id"] != callID {
			continue
		}
		content, _ := message["content"].(string)
		var value any
		if err := json.Unmarshal([]byte(content), &value); err != nil {
			t.Fatalf("decode actual directory result: %v", err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	t.Fatalf("actual provider history lacks directory result for %q", callID)
	return ""
}

func hostGrantTestResultHasPath(result []byte, path string) bool {
	var value struct {
		Grants []struct {
			Path string `json:"path"`
		} `json:"grants"`
	}
	if json.Unmarshal(result, &value) != nil {
		return false
	}
	for _, grant := range value.Grants {
		if grant.Path == path {
			return true
		}
	}
	return false
}

func hostGrantTestNoticeCount(messages []any, notice string) int {
	count := 0
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		content, _ := message["content"].(string)
		count += strings.Count(content, notice)
	}
	return count
}
