package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func appendScientificRejectedSourceFact(t *testing.T, fixture *agentSaveArtifactsFixture, gateway serverAgentRuntimeToolGateway, call agentruntime.ToolCall, ctx context.Context, kind string) {
	t.Helper()
	ctx, parent, input, request := scientificAcquisitionParent(t, fixture, gateway, call, ctx)
	if strings.HasPrefix(kind, "native_") {
		phase, err := fixture.server.newNativeScientificAcquisition(ctx, gateway.taskRun, parent, call.ID, input, request)
		if err != nil {
			t.Fatal(err)
		}
		phase.ToolResult = json.RawMessage(`{"ok":false,"error":"synthetic rejected source"}`)
		phase.ResultSHA256 = scientificSourceDigest(phase.ToolResult)
		state := "source_acquisition_failed"
		if kind == "native_corrupt" {
			state, phase.ResultSHA256 = "source_acquisition_completed", strings.Repeat("0", 64)
		}
		if err := fixture.server.checkpointNativeScientificAcquisition(ctx, parent, phase, state); err != nil {
			t.Fatal(err)
		}
		return
	}
	payload := map[string]any{"toolName": "web_fetch", "toolCallId": "ordinary-rejected", "toolPhase": kind,
		"toolInput": map[string]any{"url": request.DownloadURL}, "toolResult": map[string]any{"ok": false, "error": "synthetic rejected source"}}
	if _, _, _, err := fixture.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: fixture.claim, ClientMessageID: "ordinary-rejected-" + kind, Phase: transcriptstore.RunnerPhaseExecuting,
		PayloadJSON: mustMarshalRawMessage(payload),
	}); err != nil {
		t.Fatal(err)
	}
}

func appendScientificRealFetchedSource(t *testing.T, fixture *agentSaveArtifactsFixture, gateway serverAgentRuntimeToolGateway, ctx context.Context) {
	t.Helper()
	input := map[string]any{"url": "https://source.example.org/records.csv?release=one&part=1", "limit": nativeScientificSourceLimit}
	call := agentruntime.ToolCall{ID: "ordinary-real-fetch", Name: "web_fetch", Arguments: mustMarshalRawMessage(input)}
	result, err := gateway.Execute(ctx, call)
	if err != nil || !agentPublicScientificResultAuthorizesDownload(normalizeNativeScientificSourceResult(mustMarshalRawMessage(result.Value)), stringValue(input["url"])) {
		t.Fatalf("real canonical source fetch failed: err=%v", err)
	}
	// Persist only the genuine returned result. No source completion is recorded
	// before the controlled TLS executor returns, and no extra model call is made.
	if _, _, _, err := fixture.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: fixture.claim, ClientMessageID: "ordinary-real-fetch-completed", Phase: transcriptstore.RunnerPhaseExecuting,
		PayloadJSON: mustMarshalRawMessage(map[string]any{"toolName": call.Name, "toolCallId": call.ID,
			"toolPhase": "completed", "toolInput": input, "toolResult": result.Value}),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeScientificSourceRecency(t *testing.T) {
	for _, kind := range []string{"failed", "unavailable", "completed", "native_failed", "native_corrupt"} {
		t.Run("new_success_after_"+kind, func(t *testing.T) {
			fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true)
			appendScientificRejectedSourceFact(t, fixture, gateway, call, ctx, kind)
			appendScientificRealFetchedSource(t, fixture, gateway, ctx)
			result, err := gateway.Execute(ctx, call)
			if err != nil || mapValue(result.Value)["ok"] != true || requests.Load() != 2 ||
				mapValue(mapValue(result.Value)["download"])["source_tool_call_id"] != "ordinary-real-fetch" {
				t.Fatalf("older rejected source overrode newer genuine source: %#v err=%v requests=%d", result.Value, err, requests.Load())
			}
		})
		t.Run("latest_"+kind+"_blocks_older_success", func(t *testing.T) {
			fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true)
			appendScientificRealFetchedSource(t, fixture, gateway, ctx)
			appendScientificRejectedSourceFact(t, fixture, gateway, call, ctx, kind)
			result, err := gateway.Execute(ctx, call)
			if err == nil && mapValue(result.Value)["ok"] == true || requests.Load() != 1 {
				t.Fatalf("latest rejected source fell back or reacquired: %#v err=%v requests=%d", result.Value, err, requests.Load())
			}
			if _, err := os.Stat(filepath.Join(fixture.projectPath, "records.csv")); !os.IsNotExist(err) {
				t.Fatalf("latest rejected source published a file: %v", err)
			}
		})
	}
}

func TestNativeScientificSourceRecencyPrefersNewFetchOverOlderReadWindow(t *testing.T) {
	fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true)
	window := map[string]any{
		"content":         "1\tDataset <https://source.example.org/records.csv?release=one&part=1>",
		"file_path_scope": "original_source", "view_format": "html-readable-display-lines",
		"source_content_included": true, "source_body_sha256": strings.Repeat("a", 64),
		"source_url": "https://source.example.org/index.html", "source_version_id": "ltr-older-source-window",
		"raw_read_with": map[string]any{"version_id": "ltr-older-source-window"},
	}
	checkpoint := sessionRunnerDurableToolCheckpoint{
		ToolName: "read_file", ToolCallID: "older-source-read", ToolPhase: "completed",
		ExecutedToolInput: json.RawMessage(`{"version_id":"ltr-older-source-window"}`),
	}
	if !agentPublicScientificReadWindowAuthorizesDownload(checkpoint, window, "https://source.example.org/records.csv?release=one&part=1") {
		t.Fatal("controlled older read window does not satisfy the existing immutable-source contract")
	}
	if _, _, _, err := fixture.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: fixture.claim, ClientMessageID: "older-source-read-completed", Phase: transcriptstore.RunnerPhaseExecuting,
		PayloadJSON: mustMarshalRawMessage(map[string]any{
			"toolName": checkpoint.ToolName, "toolCallId": checkpoint.ToolCallID, "toolPhase": checkpoint.ToolPhase,
			"executedToolInput": checkpoint.ExecutedToolInput, "toolResult": window,
		}),
	}); err != nil {
		t.Fatal(err)
	}
	appendScientificRealFetchedSource(t, fixture, gateway, ctx)
	result, err := gateway.Execute(ctx, call)
	if err != nil || mapValue(result.Value)["ok"] != true || requests.Load() != 2 ||
		mapValue(mapValue(result.Value)["download"])["source_tool_call_id"] != "ordinary-real-fetch" {
		t.Fatalf("older read window replaced the newer real canonical source: result=%#v err=%v requests=%d", result.Value, err, requests.Load())
	}
}
