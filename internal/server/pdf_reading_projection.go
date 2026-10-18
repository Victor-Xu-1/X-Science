package server

import transcriptstore "x-science/internal/persistence/transcript"

// Historical position hints remain attached to their immutable user input.
// Never read the current session binding to decorate an older message.
func projectPDFReadingSnapshot(message, payload map[string]any) {
	input, _ := payload["inputData"].(map[string]any)
	if intent, _ := input["turn_intent"].(string); intent != "" {
		return
	}
	value := input[transcriptstore.PDFReadingSnapshotKey]
	if value == nil {
		return
	}
	snapshot, err := transcriptstore.DecodePDFReadingSnapshot(value)
	if err != nil {
		message["pdf_reading_context_unavailable"] = true
		return
	}
	message["pdf_reading_context"] = snapshot
}
