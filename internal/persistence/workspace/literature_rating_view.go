package workspace

import (
	"bytes"
	"encoding/json"
)

// RatingView is a read-only interpretation of metadata.rating, never another
// persisted field. Omission on a historical receipt stays omitted on replay.
type LiteratureRatingView struct {
	Known bool   `json:"known"`
	Value *int64 `json:"value,omitempty"`
}

func literatureRatingView(metadata map[string]any) *LiteratureRatingView {
	value, exists := metadata["rating"]
	if !exists {
		zero := int64(0)
		return &LiteratureRatingView{Known: true, Value: &zero}
	}
	number, ok := value.(json.Number)
	if ok {
		if rating, known := literatureMetadataInteger([]byte(number), 5); known {
			return &LiteratureRatingView{Known: true, Value: &rating}
		}
	}
	return &LiteratureRatingView{Known: false}
}

// Validate the small projection without rounding its value through float64.
// This does not infer a projection when decoding immutable legacy receipts.
func (view *LiteratureRatingView) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ErrLiteratureInvalid
	}
	for field := range fields {
		if field != "known" && field != "value" {
			return ErrLiteratureInvalid
		}
	}
	known := bytes.TrimSpace(fields["known"])
	value, exists := fields["value"]
	if bytes.Equal(known, []byte("false")) && !exists {
		*view = LiteratureRatingView{Known: false}
		return nil
	}
	if !bytes.Equal(known, []byte("true")) || !exists {
		return ErrLiteratureInvalid
	}
	rating, valid := literatureMetadataInteger(bytes.TrimSpace(value), 5)
	if !valid {
		return ErrLiteratureInvalid
	}
	*view = LiteratureRatingView{Known: true, Value: &rating}
	return nil
}
