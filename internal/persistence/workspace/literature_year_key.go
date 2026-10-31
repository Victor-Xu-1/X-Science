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
	return literatureYearNumber(raw)
}

// Recognize a mathematical integer in 0..9999 directly from a JSON number.
// In particular 2025.0001 is not 2025 even when a float64 would round it there.
// Saturating exponent scanning keeps huge signed exponents bounded by the
// already-read token size; it never builds an exponent-sized integer or string.
func literatureYearNumber(raw []byte) (int64, bool) {
	if len(raw) == 0 || raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') || !json.Valid(raw) {
		return 0, false
	}
	endMantissa := bytes.IndexAny(raw, "eE")
	if endMantissa < 0 {
		endMantissa = len(raw)
	}
	digits, fractional, first, last := 0, 0, -1, -1
	decimal := false
	for index, char := range raw[:endMantissa] {
		if char == '-' && index == 0 {
			continue
		}
		if char == '.' {
			decimal = true
			continue
		}
		if char < '0' || char > '9' {
			return 0, false
		}
		if char != '0' {
			if first < 0 {
				first = digits
			}
			last = digits
		}
		digits++
		if decimal {
			fractional++
		}
	}
	if first < 0 {
		return 0, true
	}
	if raw[0] == '-' {
		return 0, false
	}
	exponent := 0
	if endMantissa < len(raw) {
		position, negative := endMantissa+1, false
		if raw[position] == '-' || raw[position] == '+' {
			negative = raw[position] == '-'
			position++
		}
		bound := len(raw)
		for ; position < len(raw); position++ {
			digit := int(raw[position] - '0')
			if exponent > bound/10 || exponent == bound/10 && digit > bound%10 {
				exponent = bound
			} else {
				exponent = exponent*10 + digit
			}
		}
		if negative {
			exponent = -exponent
		}
	}
	endInteger := digits - fractional + exponent
	if endInteger <= last || endInteger-first < 1 || endInteger-first > 4 {
		return 0, false
	}
	var year int64
	position := 0
	for _, char := range raw[:endMantissa] {
		if char < '0' || char > '9' {
			continue
		}
		if position >= first && position < endInteger {
			year = year*10 + int64(char-'0')
		}
		position++
	}
	for ; position < endInteger; position++ {
		year *= 10
	}
	return year, true
}
