package agentruntime

// ModelUsage keeps counters and their provider-reported presence together.
// A missing counter is not a measured zero, and a locally derived total is
// not a provider-reported total. Cache counters retain the adapter's semantics.
type ModelUsage struct {
	InputTokens      int                `json:"input_tokens"`
	OutputTokens     int                `json:"output_tokens"`
	CacheReadTokens  int                `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int                `json:"cache_write_tokens,omitempty"`
	TotalTokens      int                `json:"total_tokens"`
	Reported         *ModelUsageFields  `json:"reported,omitempty"`
	InvalidFields    ModelUsageFields   `json:"invalid_fields,omitzero"`
	Warnings         ModelUsageWarnings `json:"warnings,omitempty,omitzero"`
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

// ModelUsageFromCounters preserves valid siblings without inventing absent
// values. Current invalidity and bounded historical diagnostics are separate.
func ModelUsageFromCounters(input, output, total, cacheRead, cacheWrite OptionalUsageCounter) ModelUsage {
	usage := ModelUsage{Reported: &ModelUsageFields{}}
	for _, field := range []struct {
		wire              OptionalUsageCounter
		name              UsageCounterField
		value             *int
		reported, invalid *bool
	}{
		{input, UsageFieldInput, &usage.InputTokens, &usage.Reported.Input, &usage.InvalidFields.Input},
		{output, UsageFieldOutput, &usage.OutputTokens, &usage.Reported.Output, &usage.InvalidFields.Output},
		{total, UsageFieldTotal, &usage.TotalTokens, &usage.Reported.Total, &usage.InvalidFields.Total},
		{cacheRead, UsageFieldCacheRead, &usage.CacheReadTokens, &usage.Reported.CacheRead, &usage.InvalidFields.CacheRead},
		{cacheWrite, UsageFieldCacheWrite, &usage.CacheWriteTokens, &usage.Reported.CacheWrite, &usage.InvalidFields.CacheWrite},
	} {
		if value, valid := field.wire.Value(); valid {
			*field.value, *field.reported = value, true
		} else if field.wire.State() == UsageCounterInvalid {
			*field.invalid = true
			usage.Warnings = usage.Warnings.Add(field.name, field.wire.Reason())
		}
	}
	return usage
}

// DeriveTotal never labels a local sum as reported, nor treats an invalid
// additive cache component as zero. An overflowing sum remains unavailable.
func (usage *ModelUsage) DeriveTotal(includeCache bool) {
	usage.CacheTokensAdditive = includeCache
	fields := usage.ReportedFields()
	if fields.Total || usage.InvalidFields.Total {
		return
	}
	usage.TotalTokens = 0
	if !fields.Input || !fields.Output || usage.InvalidFields.Input || usage.InvalidFields.Output {
		return
	}
	values := []int{usage.InputTokens, usage.OutputTokens}
	if includeCache {
		if usage.InvalidFields.CacheRead || usage.InvalidFields.CacheWrite {
			return
		}
		values = append(values, usage.CacheReadTokens, usage.CacheWriteTokens)
	}
	for _, value := range values {
		if value < 0 || value > int(^uint(0)>>1)-usage.TotalTokens {
			usage.TotalTokens = 0
			usage.Warnings = usage.Warnings.Add(UsageFieldTotal, UsageCounterRange)
			return
		}
		usage.TotalTokens += value
	}
}

// AddRequestUsage combines distinct requests, not cumulative stream snapshots:
// a later request cannot heal an unknown component of an earlier request.
func (usage ModelUsage) AddRequestUsage(next ModelUsage) ModelUsage {
	left, right := usage.ReportedFields(), next.ReportedFields()
	leftInvalid, rightInvalid := usage.InvalidFields, next.InvalidFields
	// Preserve each request's incomplete additive-input dependency even when
	// the combined adapters no longer share additive cache semantics.
	for _, request := range []struct {
		usage   ModelUsage
		invalid *ModelUsageFields
	}{{usage, &leftInvalid}, {next, &rightInvalid}} {
		if !request.usage.CacheTokensAdditive {
			continue
		}
		request.invalid.Input = request.invalid.Input || request.invalid.CacheRead || request.invalid.CacheWrite
		input := 0
		for _, value := range [...]int{request.usage.InputTokens, request.usage.CacheReadTokens, request.usage.CacheWriteTokens} {
			if value < 0 || value > int(^uint(0)>>1)-input {
				request.invalid.Input = true
				break
			}
			input += value
		}
	}
	result := ModelUsage{
		Reported: &ModelUsageFields{
			Input: left.Input && right.Input, Output: left.Output && right.Output,
			Total: left.Total && right.Total, CacheRead: left.CacheRead && right.CacheRead,
			CacheWrite: left.CacheWrite && right.CacheWrite,
		},
		Warnings:            usage.Warnings.Merge(next.Warnings),
		CacheTokensAdditive: usage.CacheTokensAdditive && next.CacheTokensAdditive,
	}
	for _, field := range []struct {
		left, right               int
		leftInvalid, rightInvalid bool
		name                      UsageCounterField
		value                     *int
		invalid                   *bool
	}{
		{usage.InputTokens, next.InputTokens, leftInvalid.Input, rightInvalid.Input, UsageFieldInput, &result.InputTokens, &result.InvalidFields.Input},
		{usage.OutputTokens, next.OutputTokens, leftInvalid.Output, rightInvalid.Output, UsageFieldOutput, &result.OutputTokens, &result.InvalidFields.Output},
		{usage.TotalTokens, next.TotalTokens, leftInvalid.Total, rightInvalid.Total, UsageFieldTotal, &result.TotalTokens, &result.InvalidFields.Total},
		{usage.CacheReadTokens, next.CacheReadTokens, leftInvalid.CacheRead, rightInvalid.CacheRead, UsageFieldCacheRead, &result.CacheReadTokens, &result.InvalidFields.CacheRead},
		{usage.CacheWriteTokens, next.CacheWriteTokens, leftInvalid.CacheWrite, rightInvalid.CacheWrite, UsageFieldCacheWrite, &result.CacheWriteTokens, &result.InvalidFields.CacheWrite},
	} {
		*field.invalid = field.leftInvalid || field.rightInvalid
		if field.left < 0 || field.right < 0 || field.right > int(^uint(0)>>1)-field.left {
			*field.invalid = true
			result.Warnings = result.Warnings.Add(field.name, UsageCounterRange)
			continue
		}
		*field.value = field.left + field.right
	}
	return result
}
