package agentruntime

// ModelUsage keeps counters and their provider-reported presence together.
// A missing counter is not a measured zero, and a locally derived total is
// not a provider-reported total. Cache counters retain the adapter's semantics.
type ModelUsage struct {
	InputTokens      int               `json:"input_tokens"`
	OutputTokens     int               `json:"output_tokens"`
	CacheReadTokens  int               `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int               `json:"cache_write_tokens,omitempty"`
	TotalTokens      int               `json:"total_tokens"`
	Reported         *ModelUsageFields `json:"reported,omitempty"`
	// Anthropic reports uncached input separately; other adapters already
	// include cached input. The adapter, never the display, owns this meaning.
	CacheTokensAdditive bool `json:"cache_tokens_additive,omitempty"`
}

type ModelUsageFields struct {
	Input      bool `json:"input"`
	Output     bool `json:"output"`
	CacheRead  bool `json:"cache_read"`
	CacheWrite bool `json:"cache_write"`
	Total      bool `json:"total"`
}

// ReportedFields preserves older in-process model clients without treating
// their zero-valued fields as evidence. Native adapters always supply Reported,
// including an all-false value when the response omits usage entirely.
func (usage ModelUsage) ReportedFields() ModelUsageFields {
	if usage.Reported != nil {
		return *usage.Reported
	}
	return ModelUsageFields{
		Input: usage.InputTokens > 0, Output: usage.OutputTokens > 0,
		CacheRead: usage.CacheReadTokens > 0, CacheWrite: usage.CacheWriteTokens > 0,
		Total: usage.TotalTokens > 0,
	}
}

func modelUsageTokenValue(value *int) int {
	if value != nil {
		return *value
	}
	return 0
}
