package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
)

const memoryPreferenceCaptureKey = "memoryPreferenceCapture"

// This provenance is emitted only by admission, never accepted from a caller.
// It distinguishes an omitted preference frozen by the server from an explicit
// override, so retry cannot reinterpret either after the preference changes.
type memoryPreferenceCapture struct {
	Schema      int    `json:"schema"`
	Source      string `json:"source"`
	RootFrameID string `json:"rootFrameId"`
	Mode        string `json:"mode"`
}

func hasMemoryOverride(data map[string]any) bool {
	_, snake := data["memory_mode"]
	_, camel := data["memoryMode"]
	return snake || camel
}

func freezeMemoryPreference(data, config map[string]any, mode string) (map[string]any, map[string]any) {
	inputCopy, configCopy := make(map[string]any, len(data)), make(map[string]any, len(config))
	for key, value := range data {
		inputCopy[key] = value
	}
	for key, value := range config {
		configCopy[key] = value
	}
	inputCopy["memory_mode"] = mode
	configCopy["memory_mode"] = mode
	return inputCopy, configCopy
}

func resolveMemoryPreferenceAdmission(ctx context.Context, conn *sql.Conn, stream Stream, input AppendFrameUserEventInput, eventType, origin string, prior *Event, canonical bool) (map[string]any, map[string]any, *memoryPreferenceCapture, error) {
	data, config := input.InputData, input.RuntimeConfig
	if !canonical {
		return data, config, nil, nil
	}
	if _, supplied := data[memoryPreferenceCaptureKey]; supplied {
		return nil, nil, nil, ErrEventConflict
	}
	if _, supplied := config[memoryPreferenceCaptureKey]; supplied {
		return nil, nil, nil, ErrEventConflict
	}
	_, control := data["turn_intent"]
	eligible := eventType == "user_message" && origin == "task_intent" && stream.FrameID == stream.RootFrameID && !control
	explicit := hasMemoryOverride(data) || hasMemoryOverride(config)
	if prior != nil {
		var payload map[string]json.RawMessage
		if prior.Source != EventSourcePayload {
			return data, config, nil, nil
		}
		if err := json.Unmarshal(prior.PayloadJSON, &payload); err != nil {
			return nil, nil, nil, ErrEventConflict
		}
		raw, captured := payload[memoryPreferenceCaptureKey]
		if !captured {
			return data, config, nil, nil
		} // Never derive facts for historical events.
		var capture memoryPreferenceCapture
		if !eligible || explicit || json.Unmarshal(raw, &capture) != nil || capture.Schema != 1 || capture.Source != "root_original_input" || capture.RootFrameID != stream.RootFrameID || capture.Mode != "on" && capture.Mode != "off" {
			return nil, nil, nil, ErrEventConflict
		}
		data, config = freezeMemoryPreference(data, config, capture.Mode)
		return data, config, &capture, nil
	}
	if !eligible || explicit {
		return data, config, nil, nil
	}
	preference, err := readRootSessionOriginalInput(ctx, conn, stream)
	if err != nil {
		return nil, nil, nil, err
	}
	modeJSON, found := preference["memory_mode"]
	if !found {
		return data, config, nil, nil
	}
	var mode string
	if json.Unmarshal(modeJSON, &mode) != nil || mode != "on" && mode != "off" {
		return nil, nil, nil, ErrEventConflict
	}
	var parent sql.NullString
	if err := conn.QueryRowContext(ctx, `SELECT parent_frame_id FROM frames WHERE id=?`, stream.RootFrameID).Scan(&parent); err != nil {
		return nil, nil, nil, schemaError(err)
	}
	if parent.Valid {
		return nil, nil, nil, ErrOwnerMismatch
	}
	capture := &memoryPreferenceCapture{Schema: 1, Source: "root_original_input", RootFrameID: stream.RootFrameID, Mode: mode}
	data, config = freezeMemoryPreference(data, config, mode)
	return data, config, capture, nil
}

// Shared read projection on the caller's admission transaction, not another
// preference owner. Each option keeps its own missing/legacy value policy.
func readRootSessionOriginalInput(ctx context.Context, conn *sql.Conn, stream Stream) (map[string]json.RawMessage, error) {
	var owner, project, rootID, rootRootID, raw string
	err := conn.QueryRowContext(ctx, `SELECT project.user_id,frame.project_id,frame.root_frame_id,root.root_frame_id,COALESCE(metadata.context_data,'{}')
		FROM frames frame JOIN projects project ON project.id=frame.project_id
		JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id
		LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=root.id WHERE frame.id=?`, stream.FrameID).
		Scan(&owner, &project, &rootID, &rootRootID, &raw)
	if err != nil {
		return nil, schemaError(err)
	}
	if owner != stream.OwnerID || project != stream.ProjectID || rootID != stream.RootFrameID || rootRootID != rootID {
		return nil, ErrOwnerMismatch
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &metadata) != nil {
		return nil, ErrEventConflict
	}
	original, found := metadata["_original_input"]
	if !found {
		return nil, nil
	}
	var preference map[string]json.RawMessage
	if json.Unmarshal(original, &preference) != nil {
		return nil, ErrEventConflict
	}
	return preference, nil
}
