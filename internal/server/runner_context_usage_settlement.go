package server

import "x-science/internal/agentruntime"

// Only complete provider evidence can replace the context total. Partial
// counters remain useful, but a mixed measured/estimated sum stays estimated.
func settleRunnerContextUsage(snapshot runnerContextUsage, response agentruntime.ModelResponse) (string, int, int) {
	inputEstimate := 0
	for _, row := range snapshot.InputEstimates {
		inputEstimate += row.Tokens
	}
	outputEstimate := 0
	rows, _, err := estimateRunnerRequestUsage(agentruntime.ModelRequest{Messages: []agentruntime.Message{response.Message}})
	if err == nil {
		for _, row := range rows {
			outputEstimate += row.Tokens
		}
	}
	input, output := inputEstimate, outputEstimate
	usage := response.Usage
	fields := usage.ReportedFields()
	inputKnown := fields.Input && !usage.InvalidFields.Input && usage.InputTokens >= 0
	outputKnown := fields.Output && !usage.InvalidFields.Output && usage.OutputTokens >= 0
	if inputKnown {
		input = usage.InputTokens
		if usage.CacheTokensAdditive {
			var ok bool
			if !usage.InvalidFields.CacheRead && !usage.InvalidFields.CacheWrite {
				input, ok = sumContextUsageTokens(input, usage.CacheReadTokens, usage.CacheWriteTokens)
			}
			if !ok {
				input, inputKnown = inputEstimate, false
			}
		}
	}
	if outputKnown {
		output = usage.OutputTokens
	}
	validCounters := usage.InputTokens >= 0 && usage.OutputTokens >= 0 && usage.TotalTokens >= 0 &&
		usage.CacheReadTokens >= 0 && usage.CacheWriteTokens >= 0
	knownMinimum := 0
	if inputKnown {
		knownMinimum = input
	}
	if outputKnown {
		var ok bool
		knownMinimum, ok = sumContextUsageTokens(knownMinimum, output)
		if !ok {
			validCounters = false
		}
	}
	if validCounters && fields.Total && !usage.InvalidFields.Total && usage.TotalTokens >= knownMinimum {
		// The provider total already includes cache. An absent output counter
		// remains a bounded local estimate, never an invented measured value.
		return "provider", usage.TotalTokens, min(output, usage.TotalTokens)
	}
	used, ok := sumContextUsageTokens(input, output)
	if ok {
		if validCounters && inputKnown && outputKnown && !usage.InvalidFields.Total {
			return "provider", max(used, usage.TotalTokens), output
		}
		return "estimated", used, output
	}
	// Invalid telemetry must not fail or replay an already successful call.
	return "estimated", inputEstimate + outputEstimate, outputEstimate
}

func sumContextUsageTokens(values ...int) (int, bool) {
	total := 0
	for _, value := range values {
		if value < 0 || value > int(^uint(0)>>1)-total {
			return 0, false
		}
		total += value
	}
	return total, true
}
