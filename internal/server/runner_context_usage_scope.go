package server

import "context"

type auxiliaryContextUsageKey struct{}

// Localization and other presentation-only calls use the same dynamic model
// client but are not the agent's actual context window. Their usage remains in
// provider audit; it must not replace the main request shown in the composer.
func withAuxiliaryContextUsage(ctx context.Context) context.Context {
	return context.WithValue(ctx, auxiliaryContextUsageKey{}, true)
}
