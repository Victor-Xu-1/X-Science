package server

import (
	"encoding/json"
	"strings"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
)

// This accepts only transport-level completeness, not scientific correctness or
// permission to execute. Tool proposals still pass the existing runtime gateway.
func completeDefaultProbeResponse(response agentruntime.ModelResponse) bool {
	switch strings.ToLower(strings.TrimSpace(response.StopReason)) {
	case "length", "max_tokens", "max_output_tokens", "content_filter":
		return false
	}
	for _, call := range response.Message.ToolCalls {
		var arguments map[string]json.RawMessage
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || call.ProviderProtocolDiagnostic != "" ||
			json.Unmarshal(call.Arguments, &arguments) != nil || arguments == nil {
			return false
		}
	}
	return strings.TrimSpace(response.Message.Content) != "" || len(response.Message.ToolCalls) > 0
}

// Consume before dispatch: a failed or interrupted comparison cannot repeat the
// same probe after client/process recreation. The measured Next proposal stays
// available for transport/limit negotiation, but an exhausted action window
// retires that stale numerical fallback through retireFailedPlanningProbe.
func (client *sessionOutputBudgetClient) consumeDefaultProbe(contextID string) (outputBudgetState, bool, error) {
	var state outputBudgetState
	consumed := false
	err := client.store.EditNamespace(client.namespace, func(entries map[string]runtimekv.Entry) (bool, error) {
		entry, found := entries["state"]
		if !found {
			return false, nil
		}
		var err error
		state, err = client.decode(entry)
		if err != nil || !state.DefaultProbePending {
			return false, err
		}
		state.DefaultProbePending = false
		if state.Ceiling == 0 || state.Context != contextID {
			state.DefaultProbeUsed = true
			consumed = true
		}
		entry.Value = state
		entry.Version++
		entry.UpdatedAt = time.Now().UTC()
		entries["state"] = entry
		return true, nil
	})
	return state, consumed, err
}

// An empty default-budget attempt that exhausted its action window disproves
// further automatic replay of the preceding numerical proposal. This is not a
// provider capacity observation or a successful preference. Explicit recovery
// may still request the default route; the canonical runner owns auto-resume.
func (client *sessionOutputBudgetClient) retireFailedPlanningProbe(probe outputBudgetState) error {
	return client.store.EditNamespace(client.namespace, func(entries map[string]runtimekv.Entry) (bool, error) {
		entry, found := entries["state"]
		if !found {
			return false, nil
		}
		state, err := client.decode(entry)
		if err != nil {
			return false, err
		}
		// Do not erase a newer request's observation if ownership has moved on.
		if !state.DefaultProbeUsed || state.DefaultProbePending || state.Context != probe.Context ||
			state.Next != probe.Next || state.Lower != probe.Lower || state.Ceiling != probe.Ceiling || state.Rejected != probe.Rejected {
			return false, nil
		}
		state.Next = 0
		state.SkipInitialAllowance = true
		entry.Value = state
		entry.Version++
		entry.UpdatedAt = time.Now().UTC()
		entries["state"] = entry
		return true, nil
	})
}

// Successful provider-default execution retires only the soft initial hint.
// Never rewrite the saved model profile or erase a provider-declared bound.
func (client *sessionOutputBudgetClient) acceptDefaultProbe(contextID string) error {
	return client.store.EditNamespace(client.namespace, func(entries map[string]runtimekv.Entry) (bool, error) {
		entry, found := entries["state"]
		if !found {
			return false, nil
		}
		state, err := client.decode(entry)
		if err != nil {
			return false, err
		}
		if !state.DefaultProbeUsed || state.DefaultProbePending ||
			(state.Ceiling > 0 && state.Context == contextID) {
			return false, nil
		}
		state.Next = 0
		state.SkipInitialAllowance = true
		entry.Value = state
		entry.Version++
		entry.UpdatedAt = time.Now().UTC()
		entries["state"] = entry
		return true, nil
	})
}
