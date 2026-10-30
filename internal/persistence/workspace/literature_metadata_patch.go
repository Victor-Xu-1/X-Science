package workspace

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
)

// Frozen top-level operations, independent of the caller's mutable raw bytes
// and of the current catalog object. A set value null is not a removal.
type literatureMetadataFields struct {
	Set    map[string]any `json:"set,omitempty"`
	Remove []string       `json:"remove,omitempty"`
}

func normalizeLiteratureMetadataPatch(raw json.RawMessage) (*literatureMetadataFields, json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	if len(raw) > literatureMetadataPatchBytes || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil, ErrLiteratureInvalid
	}
	var operators map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var trailing any
	if decoder.Decode(&operators) != nil || operators == nil || decoder.Decode(&trailing) != io.EOF {
		return nil, nil, ErrLiteratureInvalid
	}
	for key := range operators {
		if key != "set" && key != "remove" {
			return nil, nil, ErrLiteratureInvalid
		}
	}
	fields := &literatureMetadataFields{}
	if set := operators["set"]; set != nil {
		var err error
		fields.Set, err = decodeLiteratureMetadataObject(string(set))
		if err != nil {
			return nil, nil, ErrLiteratureInvalid
		}
	}
	if remove := operators["remove"]; remove != nil {
		var removals []json.RawMessage
		if bytes.Equal(bytes.TrimSpace(remove), []byte("null")) || json.Unmarshal(remove, &removals) != nil {
			return nil, nil, ErrLiteratureInvalid
		}
		for _, rawKey := range removals {
			var key string
			if bytes.Equal(bytes.TrimSpace(rawKey), []byte("null")) || json.Unmarshal(rawKey, &key) != nil {
				return nil, nil, ErrLiteratureInvalid
			}
			fields.Remove = append(fields.Remove, key)
		}
	}
	if len(fields.Set)+len(fields.Remove) == 0 {
		return nil, nil, ErrLiteratureInvalid
	}
	seen := make(map[string]struct{}, len(fields.Remove))
	for _, key := range fields.Remove {
		_, repeated := seen[key]
		_, set := fields.Set[key]
		if repeated || set {
			return nil, nil, ErrLiteratureInvalid
		}
		seen[key] = struct{}{}
	}
	sort.Strings(fields.Remove)
	frozen, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, ErrLiteratureInvalid
	}
	return fields, json.RawMessage(frozen), nil
}

func mergeLiteratureMetadataFields(raw string, fields *literatureMetadataFields) (string, error) {
	metadata, err := decodeLiteratureMetadataObject(raw)
	if err != nil {
		return "", err
	}
	for key, value := range fields.Set {
		metadata[key] = value
	}
	for _, key := range fields.Remove {
		delete(metadata, key)
	}
	// Preserve json.Number encoding; final admission uses the canonical prior
	// row inside the existing CAS owner, never unedited renderer metadata.
	return normalizeLiteratureMetadata(metadata)
}
