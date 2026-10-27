package workspace

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Metadata is ordinary JSON, not an implicit float64 measurement. Keep numeric
// tokens intact when reading the catalog or freezing an original receipt.
func decodeLiteratureMetadataObject(raw string) (map[string]any, error) {
	var metadata map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&metadata) != nil || metadata == nil {
		return nil, ErrLiteratureInvalid
	}
	return metadata, nil
}

// Preserve metadata numbers in typed item/receipt decoding, including immutable
// mutation-ledger replay. The alias avoids calling this method recursively.
func (item *LiteratureItem) UnmarshalJSON(raw []byte) error {
	type wireItem LiteratureItem
	var decoded wireItem
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return ErrLiteratureInvalid
	}
	for name, status := range fields {
		if !strings.EqualFold(name, "reading_status") {
			continue
		}
		var value LiteratureReadingStatus
		if bytes.Equal(bytes.TrimSpace(status), []byte("null")) || json.Unmarshal(status, &value) != nil || !validLiteratureReadingStatus(value) {
			return ErrLiteratureInvalid
		}
	}
	// Preserve an omitted field on immutable legacy acknowledgements. Current
	// catalogue reads carry the stored enum; clients project old omissions only.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*item = LiteratureItem(decoded)
	return nil
}
