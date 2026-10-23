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
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*item = LiteratureItem(decoded)
	return nil
}
