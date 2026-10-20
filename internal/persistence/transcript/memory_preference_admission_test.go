package transcript

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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

// Use the real typed admission boundary, not a loading-id string or a new API.
// The older standalone fixture intentionally omits these workspace columns.
func verifierAdmissionFixture(t *testing.T, original, legacyInput string) (*Repository, *sql.DB, Stream) {
	t.Helper()
	repo, db, stream := memoryAdmissionFixture(t, original)
	if _, err := db.Exec(`ALTER TABLE frame_runtime_metadata ADD COLUMN input_data TEXT;
		CREATE TABLE IF NOT EXISTS queued_user_messages(intent_id TEXT PRIMARY KEY,frame_id TEXT,state TEXT,payload TEXT);
		UPDATE frames SET incarnation_id='verifier-incarnation' WHERE id='memory-root'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE frame_runtime_metadata SET input_data=? WHERE frame_id='memory-root'`, legacyInput); err != nil {
		t.Fatal(err)
	}
	return repo, db, stream
}

func verifierAdmissionInput(stream Stream, loadingID string) AppendFrameUserEventInput {
	intent := &FrameMessageIntent{Version: 1, OwnerID: stream.OwnerID, FrameID: stream.FrameID,
		FrameIncarnationID: "verifier-incarnation", LoadingID: loadingID,
		InputMessageID: FrameMessageIntentID(stream.FrameID, loadingID, ""), RequestSHA256: strings.Repeat("a", 64)}
	input := memoryAdmissionInput(stream, intent.InputMessageID)
	input.MessageIntent = intent
	input.InputData = map[string]any{"request": input.Text}
	return input
}

func verifierCapturedMode(t *testing.T, event Event) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	capture, ok := payload["verifierPreferenceCapture"].(map[string]any)
	if !ok || capture["schema"] != float64(1) || capture["source"] != "root_session_preference" || capture["rootFrameId"] != "memory-root" {
		t.Fatalf("missing authoritative verifier capture: %s", event.PayloadJSON)
	}
	mode, ok := capture["mode"].(string)
	data, _ := payload["inputData"].(map[string]any)
	config, _ := payload["runtimeConfig"].(map[string]any)
	if !ok || data["verifier_mode"] != mode || config["verifierMode"] != mode {
		t.Fatalf("verifier mode not frozen in input and runtime config: %s", event.PayloadJSON)
	}
	return mode
}

func TestVerifierPreferenceAdmissionCapturesCurrentRootAndReplaysExactInput(t *testing.T) {
	repo, db, stream := verifierAdmissionFixture(t, `{"_original_input":{"verifier_mode":"on"}}`, `{}`)
	previous := memoryAdmissionInput(stream, "prior-explicit")
	previous.RuntimeConfig = map[string]any{"verifierMode": "off"}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), previous); err != nil {
		t.Fatal(err)
	}
	input := verifierAdmissionInput(stream, "omitted-verifier")
	event, _, created, err := repo.AppendFrameUserEvent(t.Context(), input)
	if err != nil || !created {
		t.Fatalf("typed admission created=%t err=%v", created, err)
	}
	if verifierCapturedMode(t, event) != "on" {
		t.Fatalf("new input did not capture current root: %s", event.PayloadJSON)
	}
	config, found, err := repo.LatestFrameRuntimeConfig(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || !found || config["verifierMode"] != "on" {
		t.Fatalf("runner still sees old explicit off: config=%#v found=%t err=%v", config, found, err)
	}
	if _, supplied := input.InputData["verifier_mode"]; supplied || input.RuntimeConfig != nil {
		t.Fatal("admission mutated the caller's immutable request maps")
	}
	if _, err := db.Exec(`UPDATE frame_runtime_metadata SET context_data='{"_original_input":{"verifier_mode":"off"}}' WHERE frame_id='memory-root'`); err != nil {
		t.Fatal(err)
	}
	retry, _, created, err := repo.AppendFrameUserEvent(t.Context(), input)
	if err != nil || created || retry.EventID != event.EventID || string(retry.PayloadJSON) != string(event.PayloadJSON) {
		t.Fatalf("retry recaptured a later preference: created=%t event=%+v err=%v", created, retry, err)
	}
	olderCaller := input
	olderCaller.MessageIntent = nil
	retry, _, created, err = repo.AppendFrameUserEvent(t.Context(), olderCaller)
	if err != nil || created || string(retry.PayloadJSON) != string(event.PayloadJSON) {
		t.Fatalf("repository replay without new transport field changed original bytes: created=%t err=%v", created, err)
	}
	next, _, _, err := repo.AppendFrameUserEvent(t.Context(), verifierAdmissionInput(stream, "next-new-input"))
	if err != nil || verifierCapturedMode(t, next) != "off" {
		t.Fatalf("next NEW input did not capture its own admission value: err=%v", err)
	}
}

func TestVerifierPreferenceAdmissionValidatesStoredPreference(t *testing.T) {
	for _, test := range []struct {
		name, original, legacy, want string
	}{
		{"default", `{}`, `{}`, "off"},
		{"legacy true", `{}`, `{"verifier_mode":true}`, "on"},
		{"legacy false", `{}`, `{"verifier_mode":false}`, "off"},
		{"trim case", `{"_original_input":{"verifier_mode":"  ON  "}}`, `{}`, "on"},
		{"original wins", `{"_original_input":{"verifier_mode":false}}`, `{"verifier_mode":"on"}`, "off"},
		{"null is invalid", `{"_original_input":{"verifier_mode":null}}`, `{"verifier_mode":"on"}`, ""},
		{"number is invalid", `{"_original_input":{"verifier_mode":42}}`, `{}`, ""},
		{"unknown is invalid", `{"_original_input":{"verifier_mode":"enabled"}}`, `{}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, _, stream := verifierAdmissionFixture(t, test.original, test.legacy)
			event, _, _, err := repo.AppendFrameUserEvent(t.Context(), verifierAdmissionInput(stream, "value-policy"))
			if test.want == "" {
				if !errors.Is(err, ErrEventConflict) {
					t.Fatalf("invalid present verifier preference admitted: event=%s err=%v", event.PayloadJSON, err)
				}
				return
			}
			if err != nil || verifierCapturedMode(t, event) != test.want {
				t.Fatalf("stored preference want=%s event=%s err=%v", test.want, event.PayloadJSON, err)
			}
		})
	}
}

func TestVerifierPreferenceAdmissionPreservesExplicitAndNonordinaryInputs(t *testing.T) {
	for _, kind := range []string{"explicit", "control", "response", "untyped"} {
		t.Run(kind, func(t *testing.T) {
			repo, _, stream := verifierAdmissionFixture(t, `{"_original_input":{"verifier_mode":42}}`, `{}`)
			input := verifierAdmissionInput(stream, "unchanged-"+kind)
			switch kind {
			case "explicit":
				input.InputData["verifier_mode"] = "on"
				input.RuntimeConfig = map[string]any{"verifierMode": "on"}
			case "control":
				input.InputData["turn_intent"] = "save-as-skill"
			case "response":
				input.MessageOrigin = "input_response"
			case "untyped":
				input.MessageIntent = nil
			}
			event, _, _, err := repo.AppendFrameUserEvent(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil || payload["verifierPreferenceCapture"] != nil {
				t.Fatalf("unrelated input acquired a verifier capture: event=%s err=%v", event.PayloadJSON, err)
			}
		})
	}
}

func TestVerifierPreferenceAdmissionRejectsClientSuppliedCapture(t *testing.T) {
	for _, surface := range []string{"input", "runtime"} {
		t.Run(surface, func(t *testing.T) {
			repo, _, stream := verifierAdmissionFixture(t, `{}`, `{}`)
			input := verifierAdmissionInput(stream, "forged-verifier-capture")
			if surface == "input" {
				input.InputData["verifierPreferenceCapture"] = map[string]any{"mode": "on"}
			} else {
				input.RuntimeConfig = map[string]any{"verifierPreferenceCapture": map[string]any{"mode": "on"}}
			}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); !errors.Is(err, ErrEventConflict) {
				t.Fatalf("caller supplied server-only verifier capture: %v", err)
			}
		})
	}
}

func TestVerifierPreferenceAdmissionKeepsTypedScopeAndCancellationGuards(t *testing.T) {
	for _, kind := range []string{"owner", "incarnation", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			repo, db, stream := verifierAdmissionFixture(t, `{"_original_input":{"verifier_mode":"on"}}`, `{}`)
			input := verifierAdmissionInput(stream, "guarded-input")
			want := ErrMessageIntentConflict
			switch kind {
			case "owner":
				want = ErrOwnerMismatch
				if _, err := db.Exec(`UPDATE projects SET user_id='another-owner' WHERE id='memory-project'`); err != nil {
					t.Fatal(err)
				}
			case "incarnation":
				if _, err := db.Exec(`UPDATE frames SET incarnation_id='replacement-incarnation' WHERE id='memory-root'`); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				want = ErrMessageIntentCancelled
				receipt := FrameMessageIntentCancellation{Intent: *input.MessageIntent, Cancelled: true, CancellationStatus: "cancelled_before_acceptance"}
				if !receipt.Valid() {
					t.Fatal("invalid cancellation fixture")
				}
				payload, err := json.Marshal(map[string]any{"_xscienceGoQueueVersion": 1, "delivery": map[string]any{"message_intent_cancellation": receipt}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO queued_user_messages(intent_id,frame_id,state,payload) VALUES(?,?,'retracted',?)`, input.ClientMessageID, stream.FrameID, string(payload)); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); !errors.Is(err, want) {
				t.Fatalf("typed %s guard changed: want=%v actual=%v", kind, want, err)
			}
		})
	}
}
