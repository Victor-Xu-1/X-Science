package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

const verifierPreferenceCaptureKey = "verifierPreferenceCapture"

// Admission alone creates this provenance. It freezes the root preference for
// one typed ordinary input, not for a later retry or a resumed historical turn.
type verifierPreferenceCapture struct {
	Schema      int    `json:"schema"`
	Source      string `json:"source"`
	RootFrameID string `json:"rootFrameId"`
	Mode        string `json:"mode"`
}

func hasVerifierOverride(values map[string]any) bool {
	_, snake := values["verifier_mode"]
	_, camel := values["verifierMode"]
	return snake || camel
}

func freezeVerifierPreference(data, config map[string]any, mode string) (map[string]any, map[string]any) {
	inputCopy, configCopy := make(map[string]any, len(data)), make(map[string]any, len(config))
	for key, value := range data {
		inputCopy[key] = value
	}
	for key, value := range config {
		configCopy[key] = value
	}
	inputCopy["verifier_mode"] = mode
	configCopy["verifierMode"] = mode
	return inputCopy, configCopy
}

func resolveVerifierPreferenceAdmission(ctx context.Context, conn *sql.Conn, stream Stream, input AppendFrameUserEventInput, eventType, origin string, prior *Event, canonical bool) (map[string]any, map[string]any, *verifierPreferenceCapture, error) {
	data, config := input.InputData, input.RuntimeConfig
	if !canonical {
		return data, config, nil, nil
	}
	if _, supplied := data[verifierPreferenceCaptureKey]; supplied {
		return nil, nil, nil, ErrEventConflict
	}
	if _, supplied := config[verifierPreferenceCaptureKey]; supplied {
		return nil, nil, nil, ErrEventConflict
	}
	_, control := data["turn_intent"]
	ordinary := eventType == "user_message" && origin == "task_intent" && stream.FrameID == stream.RootFrameID && !control
	explicit := hasVerifierOverride(data) || hasVerifierOverride(config)
	if prior != nil {
		if prior.Source != EventSourcePayload {
			return data, config, nil, nil
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(prior.PayloadJSON, &payload) != nil {
			return nil, nil, nil, ErrEventConflict
		}
		raw, captured := payload[verifierPreferenceCaptureKey]
		if !captured {
			return data, config, nil, nil // Never manufacture facts for old inputs.
		}
		var capture verifierPreferenceCapture
		if !ordinary || explicit || json.Unmarshal(raw, &capture) != nil || capture.Schema != 1 || capture.Source != "root_session_preference" || capture.RootFrameID != stream.RootFrameID || capture.Mode != "on" && capture.Mode != "off" {
			return nil, nil, nil, ErrEventConflict
		}
		data, config = freezeVerifierPreference(data, config, capture.Mode)
		return data, config, &capture, nil
	}
	// The existing typed intent guard has already validated owner/incarnation,
	// cancellation and replay. Legacy/direct/control callers keep their contract.
	if input.MessageIntent == nil || !ordinary || explicit {
		return data, config, nil, nil
	}
	preference, err := readRootSessionOriginalInput(ctx, conn, stream)
	if err != nil {
		return nil, nil, nil, err
	}
	var parent sql.NullString
	if err := conn.QueryRowContext(ctx, `SELECT parent_frame_id FROM frames WHERE id=?`, stream.RootFrameID).Scan(&parent); err != nil {
		return nil, nil, nil, schemaError(err)
	}
	if parent.Valid {
		return nil, nil, nil, ErrOwnerMismatch
	}
	rawMode, present := preference["verifier_mode"]
	if !present {
		var legacy sql.NullString
		if err := conn.QueryRowContext(ctx, `SELECT metadata.input_data FROM frames root
			LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=root.id WHERE root.id=?`, stream.RootFrameID).Scan(&legacy); err != nil {
			return nil, nil, nil, schemaError(err)
		}
		if legacy.Valid {
			var legacyInput map[string]json.RawMessage
			if json.Unmarshal([]byte(legacy.String), &legacyInput) != nil {
				return nil, nil, nil, ErrEventConflict
			}
			rawMode, present = legacyInput["verifier_mode"]
		}
	}
	mode := "off"
	if present {
		mode, err = normalizedVerifierPreference(rawMode)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	capture := &verifierPreferenceCapture{Schema: 1, Source: "root_session_preference", RootFrameID: stream.RootFrameID, Mode: mode}
	data, config = freezeVerifierPreference(data, config, mode)
	return data, config, capture, nil
}

func normalizedVerifierPreference(raw json.RawMessage) (string, error) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "", ErrEventConflict
	}
	switch value := value.(type) {
	case bool:
		if value {
			return "on", nil
		}
		return "off", nil
	case string:
		mode := strings.ToLower(strings.TrimSpace(value))
		if mode == "on" || mode == "off" {
			return mode, nil
		}
	}
	return "", ErrEventConflict
}
