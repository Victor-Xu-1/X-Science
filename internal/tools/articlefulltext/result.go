package articlefulltext

// ToolResultEnvelope supplies the existing transport-status contract without
// copying or serializing article bodies. A substantive abstract record remains
// available at its measured depth even when no open-access full text exists.
func (result Result) ToolResultEnvelope() map[string]any {
	return map[string]any{
		"status":            result.Status,
		"sourceUnavailable": result.SourceUnavailable,
		"retryable":         result.Retryable,
	}
}
