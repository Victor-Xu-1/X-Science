package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSkillLoadReceiptIdentityIsBoundedAndTamperEvident(t *testing.T) {
	receipt := skillLoadReceiptFixture(t)
	trimmed, err := NewSkillLoadReceipt("analysis-fixture", "scope", " filter ", receipt.Contracts)
	if err != nil || trimmed.InvocationKey != receipt.InvocationKey {
		t.Fatal("trimmed filter did not preserve its actual invocation semantics")
	}
	padded, err := NewSkillLoadReceipt("analysis-fixture", " scope ", "filter", receipt.Contracts)
	if err != nil || padded.InvocationKey == receipt.InvocationKey ||
		receipt.MatchesArguments("analysis-fixture", " scope ", "filter") ||
		!padded.MatchesArguments("analysis-fixture", " scope ", " filter ") {
		t.Fatal("render-distinct raw arguments acquired the same exact load identity")
	}
	for _, mutate := range []func(*SkillLoadReceipt){
		func(r *SkillLoadReceipt) { r.Version++ },
		func(r *SkillLoadReceipt) { r.Contracts[0].BundleSHA256 = strings.Repeat("e", 64) },
		func(r *SkillLoadReceipt) { r.ArgumentsSHA256 = strings.Repeat("e", 64) },
		func(r *SkillLoadReceipt) { r.Contracts = append(r.Contracts, r.Contracts[0]) },
		func(r *SkillLoadReceipt) { r.InvocationKey = strings.Repeat("A", 64) },
	} {
		changed := receipt.Clone()
		mutate(changed)
		if changed.Valid() {
			t.Fatal("tampered Skill receipt retained exact authority")
		}
	}
	clone := receipt.Clone()
	clone.Binding.ToolCallID, clone.Contracts[0].Name = "different-call", "different-name"
	if !receipt.BoundToCall("load-contract") || receipt.Contracts[0].Name != "analysis-fixture" {
		t.Fatal("receipt clone retained mutable binding or contract slices")
	}
	encoded, err := json.Marshal(struct {
		Result ToolResult
		Event  Event
	}{ToolResult{Value: "loaded contract", SkillLoadReceipt: receipt}, Event{SkillLoadReceipt: receipt}})
	if err != nil || strings.Contains(string(encoded), "closureSha256") || strings.Contains(string(encoded), "InvocationKey") {
		t.Fatal("host-only Skill identity leaked into ordinary serialized model/event fields")
	}
}

func TestEngineCarriesSkillLoadReceiptOnlyForSuccessfulHostCompletion(t *testing.T) {
	for _, outcome := range []string{"loaded", "failed", "preflight", "model-field", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			receipt := skillLoadReceiptFixture(t)
			var events []Event
			engine := Engine{
				Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
					result := ToolResult{Value: "loaded contract", SkillLoadReceipt: receipt}
					switch outcome {
					case "failed":
						result.Value = map[string]any{"ok": false, "error": "controlled failure"}
					case "preflight":
						result.Value = map[string]any{"ok": false, "executed": false, "preflight": true, "message": "not admitted"}
					case "model-field":
						result.Value = map[string]any{"ok": true, "skillLoadReceipt": receipt}
						result.SkillLoadReceipt = nil
					case "cancelled":
						cancel()
					}
					return result, nil
				}),
				OnEvent: func(event Event) { events = append(events, event) },
			}
			_, err := engine.ExecuteToolBatch(ctx, []ToolCall{{
				ID: "load-contract", Name: "skill", Arguments: json.RawMessage(`{"skill":"analysis-fixture"}`),
			}}, 0, MediaPolicy{}, 0)
			if outcome == "cancelled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled host load returned %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var terminal *SkillLoadReceipt
			for _, event := range events {
				if event.Type == EventToolCompleted || event.Type == EventToolFailed {
					terminal = event.SkillLoadReceipt
				}
			}
			if outcome == "loaded" {
				if !terminal.BoundToCall("load-contract") || terminal == receipt {
					t.Fatal("successful terminal event lost or aliased its host receipt")
				}
			} else if terminal != nil {
				t.Fatal("non-success or model result field acquired host load authority")
			}
		})
	}
}

func skillLoadReceiptFixture(t *testing.T) *SkillLoadReceipt {
	t.Helper()
	receipt, err := NewSkillLoadReceipt("analysis-fixture", "scope", "filter", []SkillContractIdentity{{
		Name: "analysis-fixture", Origin: "personal", ContractSHA256: strings.Repeat("a", 64), BundleKind: "files", BundleSHA256: strings.Repeat("b", 64),
	}})
	if err != nil {
		t.Fatal(err)
	}
	receipt.Binding = &SkillLoadBinding{
		StreamUID: "fixture-stream", OwnerID: "fixture-owner", InputRevision: 1, RunnerAttempt: 1, ToolCallID: "load-contract",
	}
	return receipt
}
