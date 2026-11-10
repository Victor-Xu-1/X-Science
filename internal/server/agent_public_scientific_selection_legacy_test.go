package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

func TestNativeScientificSourceSelectionLegacyProof(t *testing.T) {
	t.Run("historical_auto_selection_stays_closed_and_new_parent_recovers", func(t *testing.T) {
		fixture := newHistoricalScientificSelectionFixture(t, false)
		original := agentSaveArtifactResults(t, mapValue(fixture.result.Value))[0]
		replayed, err := fixture.gateway.Execute(fixture.ctx, fixture.call)
		if err == nil && mapValue(replayed.Value)["ok"] == true || fixture.requests.Load() != 1 {
			t.Fatalf("historical source was guessed from projection: result=%#v err=%v requests=%d", replayed.Value, err, fixture.requests.Load())
		}
		_, _, content, found, err := fixture.store.OpenArtifactVersionContent(stringValue(original["version_id"]))
		if err != nil || !found {
			t.Fatalf("fail-closed replay discarded the original artifact: found=%t err=%v", found, err)
		}
		_ = content.Close()
		if err := fixture.server.checkpointChatTool(SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, fixture.gateway.taskRun,
			"failed", "Historical auto-selection has no immutable proof", fixture.call.ID, "failed", map[string]any{
				"toolName": fixture.call.Name, "toolInput": json.RawMessage(fixture.call.Arguments),
				"toolResult": map[string]any{"ok": false, "error": "historical source selection proof unavailable"},
			}); err != nil {
			t.Fatal(err)
		}
		fresh := fixture.call
		fresh.ID = "new-parent-after-legacy"
		declareScientificFixtureTool(t, fixture.agentSaveArtifactsFixture, fixture.gateway, fresh, "Recovering with a current source")
		recovered, err := fixture.gateway.Execute(fixture.ctx, fresh)
		if err != nil || mapValue(recovered.Value)["ok"] != true ||
			mapValue(mapValue(recovered.Value)["download"])["source_tool_call_id"] != "ordinary-real-fetch" {
			t.Fatalf("new admitted parent could not recover from a valid real source: result=%#v err=%v", recovered.Value, err)
		}
		if artifact := agentSaveArtifactResults(t, mapValue(recovered.Value))[0]; artifact["version_id"] != original["version_id"] {
			t.Fatalf("new parent replaced unchanged committed content: original=%#v recovered=%#v", original, artifact)
		}
	})
	t.Run("historical_explicit_start_input_replays_its_real_source", func(t *testing.T) {
		fixture := newHistoricalScientificSelectionFixture(t, true)
		replayed, err := fixture.gateway.Execute(fixture.ctx, fixture.call)
		if err != nil || mapValue(replayed.Value)["ok"] != true || fixture.requests.Load() != 1 ||
			agentSaveArtifactResults(t, mapValue(replayed.Value))[0]["version_id"] != agentSaveArtifactResults(t, mapValue(fixture.result.Value))[0]["version_id"] {
			t.Fatalf("explicit immutable start/source proof was lost: result=%#v err=%v requests=%d", replayed.Value, err, fixture.requests.Load())
		}
		if _, err := os.Stat(filepath.Join(fixture.projectPath, "records.csv")); err != nil {
			t.Fatalf("explicit replay did not restore the historical committed bytes: %v", err)
		}
	})
}

// Seed historical bytes and their real canonical commit with the existing
// persistence API. No selection ever existed, and no immutable history is
// deleted or relaxed. This is historical-state setup, not a claimed download.
func newHistoricalScientificSelectionFixture(t *testing.T, explicit bool) scientificSelectedSourceFixture {
	t.Helper()
	fixture, gateway, call, ctx, requests, content := scientificAcquisitionGatewayFixture(t, true)
	appendScientificDeclaredFetchedSource(t, fixture, gateway, ctx)
	var input map[string]any
	_ = json.Unmarshal(call.Arguments, &input)
	if explicit {
		input["source_tool_call_id"] = "ordinary-real-fetch"
		call.Arguments = mustMarshalRawMessage(input)
	}
	declareScientificFixtureTool(t, fixture, gateway, call, "Starting a historical download")
	parentCtx, parent, _, request := scientificAcquisitionParent(t, fixture, gateway, call, ctx)
	request.SourceToolCallID = "ordinary-real-fetch"
	artifact, version, err := fixture.store.WriteArtifactVersionRealtime(
		workspace.WithMutationIdempotencyKey(parentCtx, "historical-source-fixture"), workspace.WriteArtifactVersionInput{
			ArtifactID: streamArtifactIDFor(fixture.stream.UID, request.Filename), ProjectID: fixture.stream.ProjectID,
			Name: request.Filename, ContentType: "text/csv", Content: strings.NewReader(content), MaxBytes: int64(len(content)),
			CreatedBy: fixture.claim.RunnerID, RootFrameID: fixture.stream.RootFrameID, FrameID: fixture.stream.FrameID,
			TranscriptAssociation: &workspace.ArtifactTranscriptAssociation{StreamUID: fixture.stream.UID,
				RunnerID: fixture.claim.RunnerID, ClaimToken: fixture.claim.ClaimToken, Attempt: fixture.claim.Attempt,
				SourceEventID: parent.SourceEventID, Relation: "consumed", ReuseCurrentVersionIfUnchanged: true},
			IsIntermediate: true, Language: agentPublicScientificArtifactLanguage,
		}, fixture.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	result := fixture.server.agentPublicScientificFileResult(fixture.stream, artifact, version, request)
	return scientificSelectedSourceFixture{agentSaveArtifactsFixture: fixture, gateway: gateway, call: call, ctx: ctx,
		requests: requests, result: agentruntime.ToolResult{Value: result}}
}
