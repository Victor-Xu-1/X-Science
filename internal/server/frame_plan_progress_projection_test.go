package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestFramePlanProgressProjectionUsesCurrentVersionStatuses(t *testing.T) {
	server, store, frameID := newGeneratedPlanProcessFixture(t)
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	metadata, found, err := store.GetFrameRuntimeMetadata(frameID)
	if err != nil || !found {
		t.Fatalf("metadata: found=%t err=%v", found, err)
	}
	planJSON, err := json.Marshal(metadata.ContextData["_plan_json"])
	if err != nil {
		t.Fatal(err)
	}
	artifact, version, err := store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: "progress-plan", ProjectID: "research-process-project", Name: "plan.json", ContentType: "application/json",
		Content: strings.NewReader(string(planJSON)), MaxBytes: 32768, RootFrameID: frameID, FrameID: frameID,
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata.ContextData["_plan_artifact_id"] = artifact.ID
	metadata.ContextData["_plan_version_id"] = version.ID
	for _, test := range []struct {
		name            string
		completed       int
		version         string
		omit            bool
		missingStatuses bool
	}{
		{name: "zero completed", version: version.ID},
		{name: "partial", completed: 1, version: version.ID},
		{name: "all completed", completed: 3, version: version.ID},
		{name: "stale metadata version", completed: 1, version: "old-version", omit: true},
		{name: "missing statuses are unknown not zero", version: version.ID, missingStatuses: true, omit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			statuses := map[string]any{"stale-step": map[string]any{"status": "completed"}}
			for index, id := range []string{"module-1", "module-2", "delivery-1"} {
				status := "pending"
				if index < test.completed {
					status = "completed"
				}
				statuses[id] = map[string]any{"status": status}
			}
			metadata.ContextData["_step_statuses"] = statuses
			if test.missingStatuses {
				delete(metadata.ContextData, "_step_statuses")
			}
			metadata.ContextData["_plan_version_id"] = test.version
			if _, err := store.SetFrameRuntimeMetadata(frameID, metadata); err != nil {
				t.Fatal(err)
			}
			response := compatJSONRequest(t, server.Handler(), http.MethodGet, "/api/frames/"+frameID, "local", nil, http.StatusOK)
			output := mapValue(response["output_data"])
			if output["plan_artifact_id"] != artifact.ID || output["plan_version_id"] != version.ID {
				t.Fatalf("durable reference lost: %#v", output)
			}
			progress := mapValue(output["plan_progress"])
			if test.omit {
				if len(progress) != 0 {
					t.Fatalf("stale metadata leaked into counts: %#v", progress)
				}
				return
			}
			if int(numberValue(progress["completed"])) != test.completed || int(numberValue(progress["total"])) != 3 ||
				progress["artifact_id"] != artifact.ID || progress["version_id"] != version.ID {
				t.Fatalf("wrong version-bound counts: %#v", progress)
			}
		})
	}
}
