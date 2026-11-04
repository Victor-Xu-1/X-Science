package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// These metadata contracts preserve JSON number tokens, rather than interpreting
// them as machine measurements. All other tools retain their existing numeric
// types. Native admission, execution identity and the Host bridge share this
// declaration so the same original request has one durable operation identity.
func toolArgumentsPreserveJSONNumbers(name string) bool {
	return name == librarySaveToInboxToolName || name == "host.library.save_to_inbox"
}

func decodeAgentRuntimeToolArguments(name string, raw []byte, input *map[string]any) error {
	if !toolArgumentsPreserveJSONNumbers(name) {
		return json.Unmarshal(raw, input)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("tool arguments require one JSON object")
	}
	return nil
}
