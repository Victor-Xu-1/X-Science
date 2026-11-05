package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	delegatefixture "x-science/internal/testsupport/delegatefixture"
)

func TestTranscriptWebReadPoolEnrichmentPreservesMixedDelegation(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	fixture, err := delegatefixture.Seed(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		content map[string]any
		kind    string
	}{
		{"delegation", map[string]any{
			"call_id": fixture.ToolUseID, "name": "Agent", "status": "completed",
			"args": map[string]any{"delegate_name": fixture.DelegateName, "child_frame_id": fixture.ChildFrameID},
		}, "subagent"},
		{"notification", map[string]any{
			"call_id": "mixed-notification", "name": "synthetic_result_reader", "status": "completed",
			"output": map[string]any{"notifications": []any{map[string]any{
				"notification_type": "child_message", "payload": map[string]any{
					"sender_frame_id": fixture.ChildFrameID, "kind": "question", "text": "synthetic question",
				},
			}}},
		}, "question"},
		{"collected", map[string]any{
			"call_id": "mixed-collected", "name": "synthetic_result_reader", "status": "completed",
			"output": map[string]any{"_collected": []any{map[string]any{
				"sender_frame_id": fixture.ChildFrameID, "status": "completed",
				"_completion_bullets": []any{"synthetic completion"},
			}}},
		}, "completion"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ordinary := map[string]any{"call_id": "ordinary-mixed", "name": "read_file",
				"status": "completed", "args": map[string]any{"path": "synthetic.txt"}, "output": "ordinary result"}
			original := cloneTranscriptWebProjectionMap(ordinary)
			content := cloneTranscriptWebProjectionMap(test.content)
			if output, found := content["output"]; found {
				raw, err := json.Marshal(output)
				if err != nil {
					t.Fatal(err)
				}
				content["output"] = string(raw)
			}
			page := []map[string]any{
				{"id": "ordinary-mixed", "type": "tool_call", "position": "left", "content": ordinary},
				{"id": "lineage-mixed", "type": "tool_call", "position": "left", "content": content},
			}
			releaseWriter := holdTranscriptWebUnrelatedWriter(t, transcriptWebReadModelServingFixture{store: store})
			defer releaseWriter()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := app.enrichTranscriptWebConversationMessages(ctx, fixture.ParentFrameID, "", page); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ordinary, original) {
				t.Fatal("lineage enrichment changed an unrelated tool's content")
			}
			if test.kind == "subagent" {
				subagent, _ := content["subagent"].(map[string]any)
				if subagent["frameId"] != fixture.ChildFrameID || subagent["delegateName"] != fixture.DelegateName {
					t.Fatalf("delegation lost canonical lineage: %#v", subagent)
				}
			} else {
				events, _ := content["subagentEvents"].([]map[string]any)
				if content["name"] != "subagent_events" || len(events) != 1 ||
					events[0]["kind"] != test.kind || events[0]["frameId"] != fixture.ChildFrameID {
					t.Fatalf("typed notification/collection projection lost: %#v", content)
				}
			}
		})
	}
}
