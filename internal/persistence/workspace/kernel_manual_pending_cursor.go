package workspace

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
)

// A cursor is a scoped read position, never an authorization credential. Every
// page still validates the live owner and incarnations in its read transaction.
type manualKernelPendingCursor struct {
	Version            int    `json:"version"`
	OwnerID            string `json:"owner_id"`
	ProjectID          string `json:"project_id"`
	FrameID            string `json:"frame_id"`
	FrameIncarnationID string `json:"frame_incarnation_id"`
	RootFrameID        string `json:"root_frame_id"`
	RootIncarnationID  string `json:"root_incarnation_id"`
	Through            int64  `json:"through"`
	Before             int64  `json:"before"`
}

func newManualKernelPendingCursor(binding ManualKernelBinding, through, before int64) manualKernelPendingCursor {
	return manualKernelPendingCursor{Version: 1, OwnerID: binding.OwnerID, ProjectID: binding.ProjectID,
		FrameID: binding.FrameID, FrameIncarnationID: binding.FrameIncarnationID,
		RootFrameID: binding.RootFrameID, RootIncarnationID: binding.RootIncarnationID,
		Through: through, Before: before}
}

func encodeManualKernelPendingCursor(cursor manualKernelPendingCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeManualKernelPendingCursor(value string, binding ManualKernelBinding) (manualKernelPendingCursor, error) {
	var cursor manualKernelPendingCursor
	if len(value) > 8192 {
		return cursor, ErrManualKernelExecutionInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, ErrManualKernelExecutionInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Version != 1 ||
		cursor.Through <= 0 || cursor.Through >= 9007199254740991 || cursor.Before <= 0 || cursor.Before > cursor.Through {
		return cursor, ErrManualKernelExecutionInvalid
	}
	if cursor != newManualKernelPendingCursor(binding, cursor.Through, cursor.Before) {
		return cursor, ErrManualKernelExecutionConflict
	}
	return cursor, nil
}
