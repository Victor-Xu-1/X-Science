package server

import (
	"testing"

	"x-science/internal/agentruntime"
)

func TestContextUsageSettlementPreservesCounterPresence(t *testing.T) {
	complete := &agentruntime.ModelUsageFields{Input: true, Output: true, Total: true}
	components := &agentruntime.ModelUsageFields{Input: true, Output: true}
	cases := []struct {
		name   string
		usage  agentruntime.ModelUsage
		source string
		used   int
		output int
	}{
		{"full-cache-already-counted", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110, CacheReadTokens: 70, Reported: complete}, "provider", 110, 10},
		{"complete-components", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, Reported: components}, "provider", 110, 10},
		{"explicit-all-zero", agentruntime.ModelUsage{Reported: complete}, "provider", 0, 0},
		{"explicit-output-zero", agentruntime.ModelUsage{Reported: &agentruntime.ModelUsageFields{Output: true}}, "estimated", 1000, 0},
		{"output-only", agentruntime.ModelUsage{OutputTokens: 10, Reported: &agentruntime.ModelUsageFields{Output: true}}, "estimated", 1010, 10},
		{"input-only", agentruntime.ModelUsage{InputTokens: 100, Reported: &agentruntime.ModelUsageFields{Input: true}}, "estimated", 108, 8},
		{"input-plus-additive-cache", agentruntime.ModelUsage{InputTokens: 100, CacheReadTokens: 20, CacheWriteTokens: 5, CacheTokensAdditive: true, Reported: &agentruntime.ModelUsageFields{Input: true, CacheRead: true, CacheWrite: true}}, "estimated", 133, 8},
		{"complete-additive-cache", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 20, CacheWriteTokens: 5, CacheTokensAdditive: true, TotalTokens: 135, Reported: components}, "provider", 135, 10},
		{"total-only", agentruntime.ModelUsage{TotalTokens: 110, Reported: &agentruntime.ModelUsageFields{Total: true}}, "provider", 110, 8},
		{"explicit-total-zero", agentruntime.ModelUsage{Reported: &agentruntime.ModelUsageFields{Total: true}}, "provider", 0, 0},
		{"missing-usage", agentruntime.ModelUsage{Reported: &agentruntime.ModelUsageFields{}}, "estimated", 1008, 8},
		{"legacy-complete", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 115}, "provider", 115, 10},
		{"legacy-output-only", agentruntime.ModelUsage{OutputTokens: 10}, "estimated", 1010, 10},
		{"legacy-zero-is-unknown", agentruntime.ModelUsage{}, "estimated", 1008, 8},
		{"invalid-negative-input", agentruntime.ModelUsage{InputTokens: -1, OutputTokens: 10, TotalTokens: 9, Reported: complete}, "estimated", 1010, 10},
		{"derived-total-cannot-override-presence", agentruntime.ModelUsage{OutputTokens: 10, TotalTokens: 10, Reported: &agentruntime.ModelUsageFields{Output: true}}, "estimated", 1010, 10},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			snapshot := runnerContextUsage{InputEstimates: []runnerContextUsageRow{{Key: "messages", Tokens: 1000}}}
			response := agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "done"}, Usage: test.usage}
			source, used, output := settleRunnerContextUsage(snapshot, response)
			if source != test.source || used != test.used || output != test.output {
				t.Fatalf("settlement=(%s,%d,%d), want=(%s,%d,%d), usage=%+v", source, used, output, test.source, test.used, test.output, test.usage)
			}
		})
	}
}

func TestModelUsageAggregationDoesNotInventMissingCounters(t *testing.T) {
	partial := agentruntime.ModelUsage{OutputTokens: 10, Reported: &agentruntime.ModelUsageFields{Output: true}}
	complete := agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 5, TotalTokens: 105, Reported: &agentruntime.ModelUsageFields{Input: true, Output: true, Total: true}}
	result := addModelUsage(partial, complete)
	fields := result.ReportedFields()
	if fields.Input || fields.Total || !fields.Output || result.OutputTokens != 15 {
		t.Fatalf("mixed usage promoted missing counters: usage=%+v fields=%+v", result, fields)
	}
	source, _, output := settleRunnerContextUsage(runnerContextUsage{InputEstimates: []runnerContextUsageRow{{Key: "messages", Tokens: 1000}}}, agentruntime.ModelResponse{Usage: result})
	if source != "estimated" || output != 15 {
		t.Fatalf("mixed usage labeled as complete: source=%s output=%d", source, output)
	}
}
