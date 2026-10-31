package workspace

import (
	"bytes"
	"encoding/json"
)

// Interpret a mathematical integer up to a small field-specific maximum.
// All work is bounded by the JSON token length, including extreme exponents.
// Year and rating share this exact primitive; neither uses float rounding.
// In particular 2025.0001 is not 2025 even when a float64 would round it there.
// Saturating exponent scanning keeps huge signed exponents bounded by the
// already-read token size; it never builds an exponent-sized integer or string.
func literatureMetadataInteger(raw []byte, maximum int64) (int64, bool) {
	if maximum < 0 || maximum > 9999 || len(raw) == 0 || raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') || !json.Valid(raw) {
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
	var value int64
	position := 0
	for _, char := range raw[:endMantissa] {
		if char < '0' || char > '9' {
			continue
		}
		if position >= first && position < endInteger {
			value = value*10 + int64(char-'0')
		}
		position++
	}
	for ; position < endInteger; position++ {
		value *= 10
	}
	return value, value <= maximum
}
