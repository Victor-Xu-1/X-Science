package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/workspace"
	"x-science/internal/toolcontract"
)

func TestRunnerDirectoryContextUsesOriginalRetainedDiscovery(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	root := t.TempDir()
	for index := range 100 {
		path := filepath.Join(root, fmt.Sprintf("%03d-%s", index, strings.Repeat("segment-", 14)))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := f.server.upsertHostGrant(f.stream.OwnerID, path, "read"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, run, call := beginDirectoryContextTool(t, f, "directory-retained")
	value, err := f.server.executeAgentPermissionTool(ctx, f.identity, call, call.Name, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if int64(len(raw)) <= runnerLargeToolResultInlineLimitBytes {
		t.Fatal("actual discovery did not cross the original externalization threshold")
	}
	engine := f.server.newAgentRuntimeEngineWithContext(ctx, SessionRunnerChatOptions{SessionID: f.stream.SessionID, OutputLimitBytes: 256 << 10})
	materialized, err := engine.MaterializeToolResult(ctx, call, value, agentruntime.ToolResultSucceeded)
	if err != nil || materialized.ResultRef == "" {
		t.Fatalf("original large-result materialization failed: %v", err)
	}
	if err := f.server.checkpointSessionRunnerToolEvent(ctx, SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}, run, agentruntime.Event{
		Type: agentruntime.EventToolCompleted, ToolCallID: call.ID, ToolName: call.Name,
		Arguments: string(call.Arguments), Result: string(materialized.JSON),
	}); err != nil {
		t.Fatal(err)
	}
	discovery, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
	if err != nil || discovery.State != "completed" || discovery.ResultSHA256 == "" {
		t.Fatalf("original canonical retained result is not available: %v", err)
	}
	descriptor, _, retained, err := toolcontract.DecodeExternalizedResult(materialized.JSON)
	if err != nil || !retained {
		t.Fatalf("original retained descriptor is not valid: %v", err)
	}
	projection := newRunnerDirectoryContext(f.server, run)
	for _, visible := range []bool{true, false} {
		t.Run(fmt.Sprintf("descriptor_visible_%t", visible), func(t *testing.T) {
			request := agentruntime.ModelRequest{Tools: agentPermissionToolSchemas()[:1], Messages: []agentruntime.Message{{Role: "user", Content: "Use the retained directory result."}}}
			if visible {
				request.Messages = append(request.Messages, agentruntime.Message{Role: "tool", ToolCallID: call.ID, Content: string(materialized.JSON)})
			}
			prepared, err := projection.prepare(t.Context(), request)
			if err != nil || len(prepared.Messages) != len(request.Messages)+1 {
				t.Fatalf("retained discovery did not produce a bounded reader: %v", err)
			}
			notice := prepared.Messages[len(prepared.Messages)-1].Content
			if !strings.Contains(notice, toolcontract.CanonicalReadWith(descriptor.VersionID)) ||
				strings.Contains(notice, root) || strings.Contains(notice, directoryMissingNotice) || strings.Contains(notice, directoryUnknownNotice) {
				t.Fatal("retained-result encoding was treated as missing/unverified or disclosed grant rows")
			}
		})
	}
	t.Run("tampered_or_oversized_descriptor", func(t *testing.T) {
		var checkpoint map[string]json.RawMessage
		if err := json.Unmarshal(discovery.PayloadJSON, &checkpoint); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(checkpoint["toolResult"], &result); err != nil {
			t.Fatal(err)
		}
		result["size_bytes"] = maxSessionRunnerReadReuseEntry + 1
		checkpoint["toolResult"], _ = json.Marshal(result)
		changed := discovery
		changed.PayloadJSON, _ = json.Marshal(checkpoint)
		if original, readWith := projection.discoveryResult(t.Context(), changed); original != nil || readWith != "" {
			t.Fatal("tampered descriptor passed its original terminal-result digest")
		}
		// Even a self-consistent adapter input stays unknown when outside the
		// bounded retained-read contract. No durable row or artifact is changed.
		canonical, err := workspace.CanonicalToolResultContextJSON(checkpoint["toolResult"])
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(canonical)
		changed.ResultSHA256 = hex.EncodeToString(digest[:])
		if original, readWith := projection.discoveryResult(t.Context(), changed); original != nil || readWith != "" {
			t.Fatal("oversized adapter input became current discovery")
		}
	})
	after, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
	if err != nil || after.EventID != discovery.EventID || string(after.PayloadJSON) != string(discovery.PayloadJSON) {
		t.Fatal("optional context projection changed or re-executed retained discovery")
	}
}
