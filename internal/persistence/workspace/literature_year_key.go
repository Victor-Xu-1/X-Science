package workspace

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"strings"

	"modernc.org/sqlite"
)

// Keep registration on the existing driver's lifecycle. An unavailable year
// projection affects only explicit year reads, never opening the workspace.
var literatureYearKeyRegistrationError = sqlite.RegisterDeterministicScalarFunction("xscience_literature_year_key_v1", 1,
	func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		metadata, ok := args[0].(string)
		if !ok {
			return nil, nil
		}
		year, known := literatureYearKey(metadata)
		if !known {
			return nil, nil
		}
		return year, nil
	})

// Read the established exact-case metadata field without changing its value or
// the opaque JSON accepted by catalog writers. RawMessage avoids float rounding
// and work proportional to an exponent's value; allocation is linear in input.
func literatureYearKey(metadata string) (int64, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(metadata), &fields) != nil {
		return 0, false
	}
	raw := bytes.TrimSpace(fields["year"])
	if len(raw) == 0 {
		return 0, false
	}
	if raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return 0, false
		}
		// Manual creation already writes decimal year strings. Do not infer a
		// year from a date, range, scientific-notation string or another field.
		text = strings.TrimSpace(text)
		if text == "" {
			return 0, false
		}
		var year int64
		for _, char := range text {
			if char < '0' || char > '9' {
				return 0, false
			}
			year = year*10 + int64(char-'0')
			if year > 9999 {
				return 0, false
			}
		}
		return year, true
	}
	return literatureMetadataInteger(raw, 9999)
}
