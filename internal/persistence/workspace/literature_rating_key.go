package workspace

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"

	"modernc.org/sqlite"
)

// Registration follows the existing SQLite driver lifecycle. Failure affects
// only explicit rating reads; it never prevents the workspace from opening.
var literatureRatingKeyRegistrationError = sqlite.RegisterDeterministicScalarFunction("xscience_literature_rating_key_v1", 1,
	func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		metadata, ok := args[0].(string)
		if !ok {
			return nil, nil
		}
		rating, known := literatureRatingKey(metadata)
		if !known {
			return nil, nil
		}
		return rating, nil
	})

// Match the canonical rating view: an absent field means unrated zero; an
// explicit invalid value remains unknown. Never round or mutate opaque JSON.
func literatureRatingKey(metadata string) (int64, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(metadata), &fields) != nil || fields == nil {
		return 0, false
	}
	raw, exists := fields["rating"]
	if !exists {
		return 0, true
	}
	return literatureMetadataInteger(bytes.TrimSpace(raw), 5)
}
