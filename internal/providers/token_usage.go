package providers

import "x-science/internal/agentruntime"

type providerTokenUsage struct {
	PromptTokens        int
	CompletionTokens    int
	CacheReadTokens     int
	CacheWriteTokens    int
	TotalTokens         int
	Reported            agentruntime.ModelUsageFields
	CacheTokensAdditive bool
}

// Optional wire counters are decoded by the existing response/event decoder.
// nil covers both omission and null; a non-nil zero is an actual reported zero.
func providerUsageCounts(input, output, total, cacheRead, cacheWrite *int) providerTokenUsage {
	var usage providerTokenUsage
	for _, field := range []struct {
		wire     *int
		value    *int
		reported *bool
	}{
		{input, &usage.PromptTokens, &usage.Reported.Input},
		{output, &usage.CompletionTokens, &usage.Reported.Output},
		{total, &usage.TotalTokens, &usage.Reported.Total},
		{cacheRead, &usage.CacheReadTokens, &usage.Reported.CacheRead},
		{cacheWrite, &usage.CacheWriteTokens, &usage.Reported.CacheWrite},
	} {
		if field.wire != nil {
			*field.value = *field.wire
			*field.reported = true
		}
	}
	return usage
}

// Stream usage fields are cumulative snapshots, not increments. An omitted
// later field must not erase an earlier receipt; explicit zero still replaces it.
func (usage *providerTokenUsage) merge(next providerTokenUsage) {
	for _, field := range []struct {
		reported bool
		value    int
		target   *int
		presence *bool
	}{
		{next.Reported.Input, next.PromptTokens, &usage.PromptTokens, &usage.Reported.Input},
		{next.Reported.Output, next.CompletionTokens, &usage.CompletionTokens, &usage.Reported.Output},
		{next.Reported.Total, next.TotalTokens, &usage.TotalTokens, &usage.Reported.Total},
		{next.Reported.CacheRead, next.CacheReadTokens, &usage.CacheReadTokens, &usage.Reported.CacheRead},
		{next.Reported.CacheWrite, next.CacheWriteTokens, &usage.CacheWriteTokens, &usage.Reported.CacheWrite},
	} {
		if field.reported {
			*field.target = field.value
			*field.presence = true
		}
	}
}

func (usage *providerTokenUsage) deriveTotal(includeCache bool) {
	usage.CacheTokensAdditive = includeCache
	if usage.Reported.Total || !usage.Reported.Input || !usage.Reported.Output {
		return
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	if includeCache {
		usage.TotalTokens += usage.CacheReadTokens + usage.CacheWriteTokens
	}
}

type openAIChatUsage struct {
	PromptTokens        *int `json:"prompt_tokens"`
	CompletionTokens    *int `json:"completion_tokens"`
	TotalTokens         *int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (wire openAIChatUsage) tokenUsage() providerTokenUsage {
	return providerUsageCounts(wire.PromptTokens, wire.CompletionTokens, wire.TotalTokens,
		wire.PromptTokensDetails.CachedTokens, nil)
}

type openAIResponsesUsage struct {
	InputTokens       *int `json:"input_tokens"`
	OutputTokens      *int `json:"output_tokens"`
	TotalTokens       *int `json:"total_tokens"`
	InputTokenDetails struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func (wire openAIResponsesUsage) tokenUsage() providerTokenUsage {
	return providerUsageCounts(wire.InputTokens, wire.OutputTokens, wire.TotalTokens,
		wire.InputTokenDetails.CachedTokens, nil)
}

type anthropicStreamUsage struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
}

func (wire anthropicStreamUsage) tokenUsage() providerTokenUsage {
	return providerUsageCounts(wire.InputTokens, wire.OutputTokens, nil,
		wire.CacheReadInputTokens, wire.CacheCreationInputTokens)
}

type geminiTokenUsage struct {
	PromptTokenCount        *int `json:"promptTokenCount"`
	CandidatesTokenCount    *int `json:"candidatesTokenCount"`
	CachedContentTokenCount *int `json:"cachedContentTokenCount"`
	TotalTokenCount         *int `json:"totalTokenCount"`
}

func (wire geminiTokenUsage) tokenUsage() providerTokenUsage {
	return providerUsageCounts(wire.PromptTokenCount, wire.CandidatesTokenCount, wire.TotalTokenCount,
		wire.CachedContentTokenCount, nil)
}
