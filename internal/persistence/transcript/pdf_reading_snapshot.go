package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

func decodePDFReadingSnapshot(value any) (*PDFReadingSnapshot, error) {
	raw, err := json.Marshal(value)
	var snapshot PDFReadingSnapshot
	if err != nil || json.Unmarshal(raw, &snapshot) != nil || snapshot.Schema != PDFReadingSnapshotSchema ||
		!snapshot.Scope.Valid() || snapshot.Revision < 0 || snapshot.Revision > 9007199254740991 ||
		snapshot.Bindings == nil || len(snapshot.Bindings) > MaxPDFReadingBindings || snapshot.PageNumber < 0 {
		return nil, ErrPDFReadingInvalid
	}
	active := snapshot.ActiveBindingID == ""
	seen, versions := map[string]bool{}, map[string]bool{}
	for _, binding := range snapshot.Bindings {
		if !binding.Valid() || seen[binding.BindingID] || versions[binding.VersionID] {
			return nil, ErrPDFReadingInvalid
		}
		seen[binding.BindingID], versions[binding.VersionID] = true, true
		if binding.BindingID == snapshot.ActiveBindingID {
			active = true
			if snapshot.PageNumber > binding.PageCount {
				return nil, ErrPDFReadingInvalid
			}
		}
	}
	if !active || snapshot.PageNumber > 0 && snapshot.ActiveBindingID == "" {
		return nil, ErrPDFReadingInvalid
	}
	return &snapshot, nil
}

// Decode immutable message evidence for display/replay only. This function
// grants no live session binding, source access, or "read completed" authority.
func DecodePDFReadingSnapshot(value any) (*PDFReadingSnapshot, error) {
	return decodePDFReadingSnapshot(value)
}

func readQueuedPDFReadingSnapshot(ctx context.Context, query MessageIntentQuery, owner, frame, clientID string, data map[string]any) (*PDFReadingSnapshot, error) {
	var raw []byte
	if err := query.QueryRowContext(ctx, `SELECT payload FROM queued_user_messages WHERE frame_id=? AND intent_id=? AND state='delivering'`,
		frame, clientID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPDFReadingInvalid
		}
		return nil, err
	}
	var envelope struct {
		Version  int `json:"_xscienceGoQueueVersion"`
		Delivery struct {
			Input map[string]any `json:"inputData"`
		} `json:"delivery"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Version != 1 {
		return nil, ErrPDFReadingInvalid
	}
	originalJSON, _ := json.Marshal(envelope.Delivery.Input)
	suppliedJSON, _ := json.Marshal(data)
	if string(originalJSON) != string(suppliedJSON) {
		return nil, ErrPDFReadingInvalid
	}
	frozen, err := decodePDFReadingSnapshot(envelope.Delivery.Input[PDFReadingSnapshotKey])
	if err != nil {
		return nil, err
	}
	supplied, err := decodePDFReadingSnapshot(data[PDFReadingSnapshotKey])
	if err != nil || !reflect.DeepEqual(frozen, supplied) {
		return nil, ErrPDFReadingInvalid
	}
	actual, err := ReadPDFReadingScope(ctx, query, owner, frame)
	if err != nil {
		return nil, err
	}
	if actual != frozen.Scope {
		return nil, ErrPDFReadingScope
	}
	if err = ValidatePDFReadingSnapshotSources(ctx, query, owner, *frozen); err != nil {
		return nil, err
	}
	return frozen, nil
}
