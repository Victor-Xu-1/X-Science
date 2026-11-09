package server

import "encoding/json"

// Artifact references have their own records. Omit them before copying while
// preserving the existing clone semantics of every retained value.
func marshalTranscriptWebMessageRecord(message map[string]any) ([]byte, error) {
	var stored map[string]any
	if message != nil {
		stored = make(map[string]any, len(message))
		for key, value := range message {
			if key != "artifact_refs" {
				stored[key] = cloneTranscriptWebProjectionValue(value)
			}
		}
	}
	return json.Marshal(stored)
}
