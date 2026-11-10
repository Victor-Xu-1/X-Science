package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeScientificSourceSelectionIntegrity(t *testing.T) {
	for _, field := range []string{"source_event", "source_digest", "source_id", "executed_input", "owner", "branch", "generation", "batch", "stripped_batch", "phase", "kind"} {
		t.Run(field, func(t *testing.T) {
			fixture := newScientificSelectedSourceFixture(t)
			selection := fixture.selection
			switch field {
			case "source_event":
				selection.SourceEventID = selection.ParentSourceEventID
			case "source_digest":
				selection.SourcePayloadSHA256 = strings.Repeat("a", 64)
			case "source_id":
				selection.SourceToolCallID = "foreign-source"
			case "executed_input":
				var changed map[string]any
				_ = json.Unmarshal(selection.ParentExecutedInput, &changed)
				changed["filename"] = "different.csv"
				selection.ParentExecutedInput = mustMarshalRawMessage(changed)
				selection.ParentExecutedSHA256 = scientificSourceDigest(selection.ParentExecutedInput)
			case "owner":
				selection.OwnerID = "foreign-owner"
			case "branch":
				selection.BranchID = "foreign-branch"
			case "generation":
				selection.BranchGeneration++
			case "batch":
				selection.ParentBatchID = "foreign-batch"
			case "stripped_batch":
				selection.ParentBatchID, selection.ParentOrdinal = "", 0
			case "phase":
				selection.ToolPhase = "source_selection_started"
			case "kind":
				selection.Kind = "unknown-selection"
			}
			selection.SelectionID = selection.identity()
			if _, err := fixture.db.Exec(`UPDATE transcript_events SET payload_json=? WHERE stream_uid=? AND event_id=?`,
				mustMarshalRawMessage(selection), fixture.stream.UID, fixture.event.Event.EventID); err != nil {
				t.Fatal(err)
			}
			result, err := fixture.gateway.Execute(fixture.ctx, fixture.call)
			if err == nil && mapValue(result.Value)["ok"] == true || fixture.requests.Load() != 2 {
				t.Fatalf("corrupt %s selection replayed or executed HTTP: result=%#v err=%v requests=%d", field, result.Value, err, fixture.requests.Load())
			}
		})
	}
	t.Run("mutable_projection_is_only_a_conflict_check", func(t *testing.T) {
		fixture := newScientificSelectedSourceFixture(t)
		artifact := agentSaveArtifactResults(t, mapValue(fixture.result.Value))[0]
		entry, found, err := fixture.server.runtimeStore.Get(artifactRuntimeNamespace, stringValue(artifact["artifact_id"]))
		if err != nil || !found {
			t.Fatalf("missing real artifact projection: found=%t err=%v", found, err)
		}
		value := mapValue(entry.Value)
		mapValue(value["publicScientificDownload"])["source_tool_call_id"] = "projection-invented-source"
		if _, err := fixture.server.runtimeStore.Set(artifactRuntimeNamespace, stringValue(artifact["artifact_id"]), value); err != nil {
			t.Fatal(err)
		}
		result, err := fixture.gateway.Execute(fixture.ctx, fixture.call)
		if err == nil && mapValue(result.Value)["ok"] == true || fixture.requests.Load() != 2 {
			t.Fatalf("mutable projection became source authority: result=%#v err=%v", result.Value, err)
		}
	})
}
