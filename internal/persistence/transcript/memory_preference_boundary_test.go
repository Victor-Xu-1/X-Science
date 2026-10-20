package transcript

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestMemoryPreferenceAdmissionExplicitAliasAndBoundaryControls(t *testing.T) {
	t.Run("old explicit retry cannot become derived", func(t *testing.T) {
		repo, _, stream := memoryAdmissionFixture(t, `{"_original_input":{"memory_mode":"off"}}`)
		input := memoryAdmissionInput(stream, "explicit")
		input.RuntimeConfig = map[string]any{"memoryMode": "on"}
		if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil {
			t.Fatal(err)
		}
		input.RuntimeConfig = nil
		if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); !errors.Is(err, ErrEventConflict) {
			t.Fatalf("explicit->derived retry: %v", err)
		}
	})
	t.Run("historical omitted stays exact", func(t *testing.T) {
		repo, db, stream := memoryAdmissionFixture(t, `{}`)
		input := memoryAdmissionInput(stream, "old")
		event, _, _, err := repo.AppendFrameUserEvent(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE frame_runtime_metadata SET context_data='{"_original_input":{"memory_mode":"on"}}' WHERE frame_id='memory-root'`); err != nil {
			t.Fatal(err)
		}
		retry, _, created, err := repo.AppendFrameUserEvent(t.Context(), input)
		if err != nil || created || string(event.PayloadJSON) != string(retry.PayloadJSON) {
			t.Fatalf("historical retry=%+v created=%t err=%v", retry, created, err)
		}
	})
	t.Run("owner mismatch", func(t *testing.T) {
		repo, db, stream := memoryAdmissionFixture(t, `{"_original_input":{"memory_mode":"on"}}`)
		if _, err := db.Exec(`UPDATE projects SET user_id='other' WHERE id='memory-project'`); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), memoryAdmissionInput(stream, "wrong-owner")); !errors.Is(err, ErrOwnerMismatch) {
			t.Fatalf("owner mismatch: %v", err)
		}
	})
	t.Run("client cannot supply provenance", func(t *testing.T) {
		repo, _, stream := memoryAdmissionFixture(t, `{}`)
		input := memoryAdmissionInput(stream, "forged")
		input.InputData = map[string]any{"memoryPreferenceCapture": map[string]any{"mode": "on"}}
		if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); !errors.Is(err, ErrEventConflict) {
			t.Fatalf("forged capture: %v", err)
		}
	})
	t.Run("child not derived", func(t *testing.T) {
		repo, db, root := memoryAdmissionFixture(t, `{"_original_input":{"memory_mode":"on"}}`)
		if _, err := db.Exec(`INSERT INTO frames(id,project_id,root_frame_id,parent_frame_id,status) VALUES('child','memory-project','memory-root','memory-root','processing')`); err != nil {
			t.Fatal(err)
		}
		child, err := repo.CreateStream(t.Context(), CreateStreamInput{UID: "frame:child", OwnerID: root.OwnerID, ExternalID: "child", SessionID: "child", Kind: StreamKindFrameRef, ProjectID: root.ProjectID, FrameID: "child", RootFrameID: root.RootFrameID, Epoch: 1})
		if err != nil {
			t.Fatal(err)
		}
		event, _, _, err := repo.AppendFrameUserEvent(t.Context(), memoryAdmissionInput(child, "child"))
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["memoryPreferenceCapture"] != nil {
			t.Fatalf("child derived: %s", event.PayloadJSON)
		}
	})
}
