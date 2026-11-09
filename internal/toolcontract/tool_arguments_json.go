package toolcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// ValidateArgumentsObject checks original argument bytes before defaults or
// map decoding can erase duplicate keys. It never canonicalizes a proposal.
func ValidateArgumentsObject(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !utf8.Valid(raw) {
		return errors.New("tool arguments require a valid UTF-8 JSON object")
	}
	return ValidateJSON(raw)
}

// ValidateJSON is the shared token-level authority for durable tool input.
// Keep the established kernel validator algorithm and error semantics intact.
func ValidateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := validateJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("kernel local operation input contains trailing JSON")
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("kernel local operation object key is invalid")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("kernel local operation input contains a duplicate key")
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("kernel local operation object is invalid")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("kernel local operation array is invalid")
		}
	default:
		return errors.New("kernel local operation JSON delimiter is invalid")
	}
	return nil
}
