package workspace

// CanonicalToolResultContextJSON uses the original durable tool-item encoding.
// Read-only context consumers compare its digest with terminal_result_sha256;
// they must not invent a second JSON normalization for externalized results.
func CanonicalToolResultContextJSON(raw []byte) ([]byte, error) {
	return canonicalToolCallBatchJSON(raw, false)
}
