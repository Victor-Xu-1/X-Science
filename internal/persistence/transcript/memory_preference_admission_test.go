package transcript

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

func memoryAdmissionFixture(t *testing.T, original string) (*Repository, *sql.DB, Stream) {
	t.Helper()
	repo, db, _ := newTranscriptRepository(t)
	if _, err := db.Exec(`ALTER TABLE frames ADD COLUMN parent_frame_id TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,user_id) VALUES('memory-project','memory-owner');
		INSERT INTO frames(id,project_id,root_frame_id,status) VALUES('memory-root','memory-project','memory-root','processing');
		INSERT INTO frame_runtime_metadata(frame_id,context_data) VALUES('memory-root',?)`, original); err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(t.Context(), CreateStreamInput{UID: "frame:memory-root", OwnerID: "memory-owner", ExternalID: "memory-root", SessionID: "memory-root", Kind: StreamKindFrameRef, FrameID: "memory-root", RootFrameID: "memory-root", ProjectID: "memory-project", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	return repo, db, stream
}

func memoryAdmissionInput(stream Stream, id string) AppendFrameUserEventInput {
	return AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: id, FrameEventID: id, MessageUUID: id, Text: "Review fixture data", MessageOrigin: "task_intent"}
}

func TestMemoryPreferenceAdmissionCapturesAndReplaysExactOmittedInput(t *testing.T) {
	repo, db, stream := memoryAdmissionFixture(t, `{"_original_input":{"memory_mode":"off"}}`)
	input := memoryAdmissionInput(stream, "derived")
	event, _, created, err := repo.AppendFrameUserEvent(t.Context(), input)
	if err != nil || !created {
		t.Fatalf("created=%t err=%v", created, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	capture, ok := payload["memoryPreferenceCapture"].(map[string]any)
	if !ok || capture["schema"] != float64(1) || capture["source"] != "root_original_input" || capture["rootFrameId"] != stream.RootFrameID || capture["mode"] != "off" {
		t.Fatalf("missing authoritative capture: %s", event.PayloadJSON)
	}
	for _, key := range []string{"inputData", "runtimeConfig"} {
		value, ok := payload[key].(map[string]any)
		if !ok || value["memory_mode"] != "off" {
			t.Fatalf("%s not frozen: %s", key, event.PayloadJSON)
		}
	}
	if _, err := db.Exec(`UPDATE frame_runtime_metadata SET context_data='{"_original_input":{"memory_mode":"on"}}' WHERE frame_id='memory-root'`); err != nil {
		t.Fatal(err)
	}
	retry, _, created, err := repo.AppendFrameUserEvent(t.Context(), input)
	if err != nil || created || retry.EventID != event.EventID || string(retry.PayloadJSON) != string(event.PayloadJSON) {
		t.Fatalf("retry changed captured input: created=%t event=%+v err=%v", created, retry, err)
	}
	input.RuntimeConfig = map[string]any{"memoryMode": "off"}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("derived retry became explicit: %v", err)
	}
}

func TestMemoryPreferenceAdmissionPreservesExplicitAndNonordinaryInputs(t *testing.T) {
	for _, test := range []struct {
		name     string
		original string
		input    map[string]any
		config   map[string]any
		origin   string
	}{
		{name: "explicit camel", original: `{"_original_input":{"memory_mode":"off"}}`, config: map[string]any{"memoryMode": "on"}},
		{name: "explicit input", original: `{"_original_input":{"memory_mode":"off"}}`, input: map[string]any{"memory_mode": "on"}},
		{name: "control", original: `{"_original_input":{"memory_mode":"off"}}`, input: map[string]any{"turn_intent": "save-as-skill"}},
		{name: "response", original: `{"_original_input":{"memory_mode":"off"}}`, origin: "input_response"},
		{name: "missing", original: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, _, stream := memoryAdmissionFixture(t, test.original)
			input := memoryAdmissionInput(stream, "unchanged")
			input.InputData, input.RuntimeConfig = test.input, test.config
			if test.origin != "" {
				input.MessageOrigin = test.origin
			}
			event, _, _, err := repo.AppendFrameUserEvent(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["memoryPreferenceCapture"] != nil {
				t.Fatalf("unexpected capture: %s", event.PayloadJSON)
			}
		})
	}
}

func TestMemoryPreferenceAdmissionRejectsInvalidPersistedMode(t *testing.T) {
	for _, mode := range []string{`null`, `true`, `"enabled"`, `42`} {
		t.Run(mode, func(t *testing.T) {
			repo, _, stream := memoryAdmissionFixture(t, `{"_original_input":{"memory_mode":`+mode+`}}`)
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), memoryAdmissionInput(stream, "invalid")); err == nil {
				t.Fatal("invalid persisted mode silently admitted")
			}
		})
	}
}
