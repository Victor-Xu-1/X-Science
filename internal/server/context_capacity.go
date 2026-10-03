package server

import (
	"math"
	"synon-go/internal/providers"
)

// Zero is unknown, not a default capacity. This declaration is model-scoped;
// provider usage receipts measure tokens, not a provider's physical capacity.
type runnerContextCapacity struct {
	Tokens int
	Source string
}

func contextCapacityForModel(profile *providers.ModelProfile, options SessionRunnerChatOptions) runnerContextCapacity {
	capacity := runnerContextCapacity{Source: "unknown"}
	if profile != nil && profile.ContextWindow != nil && *profile.ContextWindow > 0 && *profile.ContextWindow <= 10_000_000 {
		capacity = runnerContextCapacity{Tokens: *profile.ContextWindow, Source: "model_profile"}
	}
	for _, key := range []string{"contextWindow", "context_window", "contextLimit", "context_limit"} {
		if raw, ok := options.RuntimeSessionConfig[key].(float64); ok && (math.IsNaN(raw) || math.IsInf(raw, 0) || raw != math.Trunc(raw)) {
			continue
		}
		if value := numberValue(options.RuntimeSessionConfig[key]); value >= 1 && value <= 10_000_000 {
			// An explicit session budget may be more conservative, never enlarge
			// a declared model window. Neither source is provider-verified.
			if capacity.Tokens == 0 || int(value) < capacity.Tokens {
				capacity = runnerContextCapacity{Tokens: int(value), Source: "configured"}
			}
			break
		}
	}
	return capacity
}

func runnerContextWindow(options SessionRunnerChatOptions) int {
	return contextCapacityForModel(options.ModelProfile, options).Tokens
}
