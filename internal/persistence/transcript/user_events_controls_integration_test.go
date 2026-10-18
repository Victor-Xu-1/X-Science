package transcript

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestGenericUserBoundaryPreservesMaintenanceAndReadingOwners(t *testing.T) {
	for _, test := range []struct {
		name string
		data map[string]any
		want error
	}{
		{name: "ordinary", data: map[string]any{"request": "Keep an ordinary input ordinary."}},
		{name: "manual", data: map[string]any{"turn_intent": ContextCompactionIntent}, want: ErrContextCompactionInvalid},
		{name: "reading_selection", data: map[string]any{PDFReadingSelectionKey: map[string]any{"schema": "untrusted"}}, want: ErrPDFReadingInvalid},
		{name: "reading_snapshot", data: map[string]any{PDFReadingSnapshotKey: map[string]any{"schema": "untrusted"}}, want: ErrPDFReadingInvalid},
		{name: "both", data: map[string]any{"turn_intent": ContextCompactionIntent, PDFReadingSnapshotKey: map[string]any{"schema": "untrusted"}}, want: ErrContextCompactionInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"text": "An input.", "inputData": test.data})
			if err != nil {
				t.Fatal(err)
			}
			_, err = normalizeAppendUserEventInput(AppendUserEventInput{
				StreamUID: "scope-stream", OwnerID: "scope-owner", ClientMessageID: "scope-message", PayloadJSON: raw,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("generic admission error=%v, want %v", err, test.want)
			}
		})
	}
}
