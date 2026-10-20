package transcript

import (
	"encoding/json"
	"testing"
)

func TestMemoryPreferenceNullContainerLeavesOmittedInputUnchanged(t *testing.T) {
	repo, _, stream := memoryAdmissionFixture(t, `{"_original_input":null}`)
	event, _, created, err := repo.AppendFrameUserEvent(t.Context(), memoryAdmissionInput(stream, "null-container"))
	if err != nil || !created {
		t.Fatalf("null preference blocked input: created=%t err=%v", created, err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"memoryPreferenceCapture", "inputData", "runtimeConfig"} {
		if payload[key] != nil {
			t.Fatalf("null preference manufactured %s: %s", key, event.PayloadJSON)
		}
	}
}
