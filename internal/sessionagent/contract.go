package sessionagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"unicode"
)

const MaxRevision int64 = 1<<53 - 1

const InputReferenceKey = "specialist_binding"

func InputReferenceFrom(data map[string]any) (*InputReference, error) {
	value, exists := data[InputReferenceKey]
	if !exists {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	var reference InputReference
	if err != nil || DecodeRecord(raw, &reference) != nil || !ValidScope(reference.Scope) || reference.Revision < 1 || reference.Revision > MaxRevision || reference.SnapshotID == "" ||
		reference.Branch.Generation < 0 || (reference.Branch.ID == "") != (reference.Branch.Generation == 0) {
		return nil, ErrInvalid
	}
	return &reference, nil
}

func validText(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func ValidScope(scope Scope) bool {
	return validText(scope.FrameID, 512) && validText(scope.FrameIncarnationID, 512) &&
		validText(scope.RootFrameID, 512) && validText(scope.RootIncarnationID, 512)
}

func ValidateCommand(command Command) error {
	if command.Schema != CommandSchema || !validText(command.CommandID, 256) || !ValidScope(command.Scope) ||
		command.ExpectedRevision < 0 || command.ExpectedRevision >= MaxRevision || (!command.UseDefault && !validText(command.DesiredAgent, 128)) || command.UseDefault && command.DesiredAgent != "" ||
		command.Branch.Generation < 0 || (command.Branch.ID == "") != (command.Branch.Generation == 0) ||
		command.Branch.ID != "" && !validText(command.Branch.ID, 512) {
		return ErrInvalid
	}
	return nil
}

// SealSnapshot is called on a server-constructed whitelist value, never an
// arbitrary provider/defaults object. Round-trip copying also severs live maps
// or slice aliases before the prepared snapshot becomes immutable authority.
func SealSnapshot(input Snapshot) (Snapshot, error) {
	input.Schema, input.ID = SnapshotSchema, ""
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 2<<20 {
		return Snapshot{}, ErrInvalid
	}
	var result Snapshot
	if DecodeRecord(raw, &result) != nil || !validText(result.AgentName, 128) ||
		!validText(result.SelectionName, 128) ||
		!validText(result.DefinitionID, 512) || !validText(result.AssistantID, 256) ||
		result.Bundled == nil && result.Profile == nil {
		return Snapshot{}, ErrInvalid
	}
	digest := sha256.Sum256(raw)
	result.ID = "agent-snapshot:" + hex.EncodeToString(digest[:])
	return result, nil
}

func ValidateSnapshot(snapshot Snapshot) error {
	if snapshot.Schema != SnapshotSchema || snapshot.ID == "" {
		return ErrUnavailable
	}
	sealed, err := SealSnapshot(snapshot)
	if err != nil || sealed.ID != snapshot.ID {
		return ErrUnavailable
	}
	return nil
}

func DecodeRecord(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > 3<<20 {
		return ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrUnavailable
	}
	return nil
}

func RecordID(kind string, values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return "delegated:specialist-" + kind + ":" + hex.EncodeToString(digest[:])
}

func ReservedEvent(kind string) bool {
	switch kind {
	case HeadEventType, ReceiptEventType, SnapshotEventType, InputEventType, ClaimEventType, AppliedEventType:
		return true
	}
	return false
}
