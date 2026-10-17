package agentruntime

import "testing"

func TestModelUsageAggregationRejectsContradictoryTotals(t *testing.T) {
	complete := &ModelUsageFields{Input: true, Output: true, Total: true}
	maximum := int(^uint(0) >> 1)
	history := ModelUsageWarnings(0).Add(UsageFieldCacheRead, UsageCounterType)
	for _, test := range []struct {
		name    string
		usage   ModelUsage
		invalid bool
	}{
		{"invalid-cache-preserves-base-bound", ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 50, Reported: complete, CacheTokensAdditive: true, InvalidFields: ModelUsageFields{CacheRead: true}}, true},
		{"valid-cache-sibling-preserves-bound", ModelUsage{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 20, TotalTokens: 110, Reported: &ModelUsageFields{Input: true, Output: true, Total: true, CacheRead: true}, CacheTokensAdditive: true, InvalidFields: ModelUsageFields{CacheWrite: true}}, true},
		{"complete-counter-contradiction", ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 50, Reported: complete}, true},
		{"additive-bound-overflow", ModelUsage{InputTokens: maximum, CacheReadTokens: 1, TotalTokens: maximum, Reported: &ModelUsageFields{Input: true, Output: true, Total: true, CacheRead: true}, CacheTokensAdditive: true}, true},
		{"independent-total", ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110, Reported: complete, CacheTokensAdditive: true, InvalidFields: ModelUsageFields{CacheRead: true}}, false},
		{"healed-current-cache-with-history", ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110, Reported: complete, CacheTokensAdditive: true, Warnings: history}, false},
	} {
		for _, side := range []string{"left", "right"} {
			t.Run(test.name+"/"+side, func(t *testing.T) {
				// A second request's oversized but independently consistent total
				// must not hide the first request's contradictory receipt.
				other := ModelUsage{TotalTokens: 1000, Reported: complete}
				if test.name == "additive-bound-overflow" {
					other.TotalTokens = 0
				}
				before := test.usage
				combined := test.usage.AddRequestUsage(other)
				if side == "right" {
					combined = other.AddRequestUsage(test.usage)
				}
				if combined.InvalidFields.Total != test.invalid || !combined.ReportedFields().Total {
					t.Fatalf("total invalidity=%t reported=%t, want invalid=%t", combined.InvalidFields.Total, combined.ReportedFields().Total, test.invalid)
				}
				if test.usage.InvalidFields != before.InvalidFields || test.usage.Warnings != before.Warnings || combined.Warnings != before.Warnings {
					t.Fatal("consistency check mutated source receipt or historical diagnostics")
				}
			})
		}
	}
}
