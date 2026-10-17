package providers

import (
	"encoding/json"
	"testing"

	"x-science/internal/agentruntime"
)

func usageObjectTarget(protocol string) (any, func() providerTokenUsage) {
	switch protocol {
	case ProtocolOpenAIResponses:
		wire := &openAIResponsesUsage{}
		return wire, func() providerTokenUsage { return wire.tokenUsage() }
	case ProtocolAnthropic:
		wire := &anthropicStreamUsage{}
		return wire, func() providerTokenUsage { return wire.tokenUsage() }
	case ProtocolGemini:
		wire := &geminiTokenUsage{}
		return wire, func() providerTokenUsage { return wire.tokenUsage() }
	default:
		wire := &openAIChatUsage{}
		return wire, func() providerTokenUsage { return wire.tokenUsage() }
	}
}

func usageObjectFields(protocol string) agentruntime.ModelUsageFields {
	return agentruntime.ModelUsageFields{
		Input: true, Output: true, Total: protocol != ProtocolAnthropic,
		CacheRead: true, CacheWrite: protocol == ProtocolAnthropic,
	}
}

func TestProviderUsageObjectShapesAndSnapshotValidity(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolOpenAIResponses, ProtocolAnthropic, ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			target, counts := usageObjectTarget(protocol)
			decode := func(raw string) providerTokenUsage {
				t.Helper()
				if err := json.Unmarshal([]byte(raw), target); err != nil {
					t.Fatalf("optional usage object rejected primary envelope: %v", err)
				}
				return counts()
			}
			supported := usageObjectFields(protocol)
			for _, raw := range []string{`[]`, `"private-usage-junk"`, `false`, `17`} {
				invalid := decode(raw)
				if invalid.InvalidFields != supported || invalid.Reported != (agentruntime.ModelUsageFields{}) || invalid.Warnings.Empty() {
					t.Fatalf("invalid object escaped its supported counter domain: %+v", invalid)
				}
				for _, reset := range []string{"null", "{}"} {
					fresh := decode(reset)
					if fresh.InvalidFields != (agentruntime.ModelUsageFields{}) || fresh.Reported != (agentruntime.ModelUsageFields{}) || !fresh.Warnings.Empty() {
						t.Fatalf("receiver reused stale object state: %+v", fresh)
					}
					invalid.merge(fresh)
					if invalid.InvalidFields != supported || invalid.Reported != (agentruntime.ModelUsageFields{}) {
						t.Fatal("null/missing stream fields healed prior invalidity")
					}
				}
				warnings := invalid.Warnings
				validRaw, err := json.Marshal(usagePresenceWire(protocol, "complete", 0, 0, 0, 0))
				if err != nil {
					t.Fatal(err)
				}
				invalid.merge(decode(string(validRaw)))
				if invalid.InvalidFields != (agentruntime.ModelUsageFields{}) || invalid.Reported != supported || invalid.Warnings != warnings {
					t.Fatalf("valid zero did not heal current state separately from historical warnings: %+v", invalid)
				}
				invalid.merge(decode(raw))
				if invalid.InvalidFields != supported || invalid.Reported != (agentruntime.ModelUsageFields{}) || invalid.Warnings != warnings {
					t.Fatal("later invalid container retained stale measured counters or changed warning history")
				}
			}
		})
	}
}

func TestProviderUsageDetailsObjectPreservesIndependentCounters(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolOpenAIResponses} {
		t.Run(protocol, func(t *testing.T) {
			target, counts := usageObjectTarget(protocol)
			wire := usagePresenceWire(protocol, "complete", 20, 3, 23, 5)
			key := "prompt_tokens_details"
			if protocol == ProtocolOpenAIResponses {
				key = "input_tokens_details"
			}
			wire[key] = []any{"private-usage-junk"}
			raw, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, target); err != nil {
				t.Fatal(err)
			}
			usage := counts()
			usage.deriveTotal(false)
			if usage.InvalidFields != (agentruntime.ModelUsageFields{CacheRead: true}) ||
				usage.Reported != (agentruntime.ModelUsageFields{Input: true, Output: true, Total: true}) ||
				usage.PromptTokens != 20 || usage.CompletionTokens != 3 || usage.TotalTokens != 23 {
				t.Fatalf("details object contaminated independent counters: %+v", usage)
			}
			minimum, known := usage.modelUsage().KnownMinimumTokens()
			if !known || minimum != 23 {
				t.Fatal("invalid overlapping cache erased the independently known base bound")
			}
			wire["total_tokens"] = 20
			raw, err = json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, target); err != nil {
				t.Fatal(err)
			}
			contradictory := counts().modelUsage()
			minimum, known = contradictory.KnownMinimumTokens()
			if !known || minimum != 23 || !contradictory.ReportedFields().Total || contradictory.TotalTokens >= minimum {
				t.Fatal("bad details concealed a contradictory provider total")
			}
		})
	}
	// Exercise the nested receiver itself, not just a fresh parent object.
	var wire openAIChatUsage
	for _, raw := range []string{`false`, `{"cached_tokens":0}`, `null`} {
		if err := json.Unmarshal([]byte(raw), &wire.PromptTokensDetails); err != nil {
			t.Fatal(err)
		}
	}
	if wire.PromptTokensDetails.CachedTokens.State() != agentruntime.UsageCounterMissing {
		t.Fatal("null nested receiver retained prior measurement")
	}
}
