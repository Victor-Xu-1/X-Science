package agentruntime

// KnownMinimumTokens retains independently valid contributions even when the
// full input is unknown. Overlapping cache is already included in input;
// additive cache contributes only its valid, reported pieces. An overflowing
// minimum cannot be represented by any valid total counter.
func (usage ModelUsage) KnownMinimumTokens() (int, bool) {
	fields := usage.ReportedFields()
	minimum := 0
	for _, field := range [...]struct {
		value             int
		reported, invalid bool
	}{
		{usage.InputTokens, fields.Input, usage.InvalidFields.Input},
		{usage.OutputTokens, fields.Output, usage.InvalidFields.Output},
		{usage.CacheReadTokens, usage.CacheTokensAdditive && fields.CacheRead, usage.InvalidFields.CacheRead},
		{usage.CacheWriteTokens, usage.CacheTokensAdditive && fields.CacheWrite, usage.InvalidFields.CacheWrite},
	} {
		if !field.reported || field.invalid {
			continue
		}
		if field.value < 0 || field.value > int(^uint(0)>>1)-minimum {
			return 0, false
		}
		minimum += field.value
	}
	return minimum, true
}
