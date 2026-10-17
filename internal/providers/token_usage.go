package providers

import "x-science/internal/agentruntime"

type providerTokenUsage struct {
	PromptTokens        int
	CompletionTokens    int
	CacheReadTokens     int
	CacheWriteTokens    int
	TotalTokens         int
	Reported            agentruntime.ModelUsageFields
	InvalidFields       agentruntime.ModelUsageFields
	Warnings            agentruntime.ModelUsageWarnings
	CacheTokensAdditive bool
}

// All wire protocols and the direct adapter use the same scalar classification.
func providerUsageCounts(input, output, total, cacheRead, cacheWrite agentruntime.OptionalUsageCounter) providerTokenUsage {
	usage := agentruntime.ModelUsageFromCounters(input, output, total, cacheRead, cacheWrite)
	return providerTokenUsage{
		PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens,
		TotalTokens: usage.TotalTokens, CacheReadTokens: usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens, Reported: *usage.Reported,
		InvalidFields: usage.InvalidFields, Warnings: usage.Warnings,
	}
}

// Stream usage fields are cumulative snapshots, not increments. An omitted
// later field must not erase an earlier receipt; explicit zero still replaces it.
func (usage *providerTokenUsage) merge(next providerTokenUsage) {
	for _, field := range []struct {
		reported, invalid       bool
		value                   int
		target                  *int
		presence, invalidTarget *bool
	}{
		{next.Reported.Input, next.InvalidFields.Input, next.PromptTokens, &usage.PromptTokens, &usage.Reported.Input, &usage.InvalidFields.Input},
		{next.Reported.Output, next.InvalidFields.Output, next.CompletionTokens, &usage.CompletionTokens, &usage.Reported.Output, &usage.InvalidFields.Output},
		{next.Reported.Total, next.InvalidFields.Total, next.TotalTokens, &usage.TotalTokens, &usage.Reported.Total, &usage.InvalidFields.Total},
		{next.Reported.CacheRead, next.InvalidFields.CacheRead, next.CacheReadTokens, &usage.CacheReadTokens, &usage.Reported.CacheRead, &usage.InvalidFields.CacheRead},
		{next.Reported.CacheWrite, next.InvalidFields.CacheWrite, next.CacheWriteTokens, &usage.CacheWriteTokens, &usage.Reported.CacheWrite, &usage.InvalidFields.CacheWrite},
	} {
		if field.reported {
			*field.target = field.value
			*field.presence = true
			*field.invalidTarget = false
		} else if field.invalid {
			*field.target = 0
			*field.presence = false
			*field.invalidTarget = true
		}
	}
	usage.Warnings = usage.Warnings.Merge(next.Warnings)
}

func (usage *providerTokenUsage) deriveTotal(includeCache bool) {
	counters := usage.modelUsage()
	counters.DeriveTotal(includeCache)
	usage.CacheTokensAdditive = includeCache
	usage.TotalTokens, usage.Warnings = counters.TotalTokens, counters.Warnings
}

func (usage providerTokenUsage) modelUsage() agentruntime.ModelUsage {
	return agentruntime.ModelUsage{
		InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens,
		TotalTokens: usage.TotalTokens, CacheReadTokens: usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens, Reported: &usage.Reported,
		InvalidFields: usage.InvalidFields, Warnings: usage.Warnings,
		CacheTokensAdditive: usage.CacheTokensAdditive,
	}
}

type openAIChatUsage struct {
	PromptTokens        agentruntime.OptionalUsageCounter `json:"prompt_tokens"`
	CompletionTokens    agentruntime.OptionalUsageCounter `json:"completion_tokens"`
	TotalTokens         agentruntime.OptionalUsageCounter `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens agentruntime.OptionalUsageCounter `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (wire openAIChatUsage) tokenUsage() providerTokenUsage {
	return providerUsageCounts(wire.PromptTokens, wire.CompletionTokens, wire.TotalTokens,
		wire.PromptTokensDetails.CachedTokens, agentruntime.OptionalUsageCounter{})
}

type openAIResponsesUsage struct {
	InputTokens       agentruntime.OptionalUsageCounter `json:"input_tokens"`
	OutputTokens      agentruntime.OptionalUsageCounter `json:"output_tokens"`
	TotalTokens       agentruntime.OptionalUsageCounter `json:"total_tokens"`
	InputTokenDetails struct {
		CachedTokens agentruntime.OptionalUsageCounter `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func (wire openAIResponsesUsage) tokenUsage() providerTokenUsage {
	return providerUsageCounts(wire.InputTokens, wire.OutputTokens, wire.TotalTokens,
		wire.InputTokenDetails.CachedTokens, agentruntime.OptionalUsageCounter{})
}

type anthropicStreamUsage struct {
	InputTokens              agentruntime.OptionalUsageCounter `json:"input_tokens"`
	OutputTokens             agentruntime.OptionalUsageCounter `json:"output_tokens"`
	CacheReadInputTokens     agentruntime.OptionalUsageCounter `json:"cache_read_input_tokens"`
	CacheCreationInputTokens agentruntime.OptionalUsageCounter `json:"cache_creation_input_tokens"`
}

func (wire anthropicStreamUsage) tokenUsage() providerTokenUsage {
	return providerUsageCounts(wire.InputTokens, wire.OutputTokens, agentruntime.OptionalUsageCounter{},
		wire.CacheReadInputTokens, wire.CacheCreationInputTokens)
}

type geminiTokenUsage struct {
	PromptTokenCount        agentruntime.OptionalUsageCounter `json:"promptTokenCount"`
	CandidatesTokenCount    agentruntime.OptionalUsageCounter `json:"candidatesTokenCount"`
	CachedContentTokenCount agentruntime.OptionalUsageCounter `json:"cachedContentTokenCount"`
	TotalTokenCount         agentruntime.OptionalUsageCounter `json:"totalTokenCount"`
}

func (wire geminiTokenUsage) tokenUsage() providerTokenUsage {
	return providerUsageCounts(wire.PromptTokenCount, wire.CandidatesTokenCount, wire.TotalTokenCount,
		wire.CachedContentTokenCount, agentruntime.OptionalUsageCounter{})
}
