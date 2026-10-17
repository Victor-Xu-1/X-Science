package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"x-science/internal/agentruntime"
	runtimekv "x-science/internal/persistence/runtimekv"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/providers"
)

func TestRunnerContextUsageCurrentInvalidity(t *testing.T) {
	components := &agentruntime.ModelUsageFields{Input: true, Output: true}
	complete := &agentruntime.ModelUsageFields{Input: true, Output: true, Total: true}
	warnings := agentruntime.ModelUsageWarnings(0).Add(agentruntime.UsageFieldCacheRead, agentruntime.UsageCounterType)
	for _, test := range []struct {
		name         string
		usage        agentruntime.ModelUsage
		source       string
		used, output int
	}{
		{"invalid-output", agentruntime.ModelUsage{InputTokens: 100, Reported: &agentruntime.ModelUsageFields{Input: true}, InvalidFields: agentruntime.ModelUsageFields{Output: true}}, "estimated", 108, 8},
		{"invalid-input", agentruntime.ModelUsage{OutputTokens: 10, Reported: &agentruntime.ModelUsageFields{Output: true}, InvalidFields: agentruntime.ModelUsageFields{Input: true}}, "estimated", 1010, 10},
		{"invalid-explicit-total", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, Reported: components, InvalidFields: agentruntime.ModelUsageFields{Total: true}}, "estimated", 110, 10},
		{"invalid-additive-cache", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, Reported: components, CacheTokensAdditive: true, InvalidFields: agentruntime.ModelUsageFields{CacheRead: true}, Warnings: warnings}, "estimated", 1010, 10},
		{"independent-explicit-total", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110, Reported: complete, CacheTokensAdditive: true, InvalidFields: agentruntime.ModelUsageFields{CacheRead: true}, Warnings: warnings}, "provider", 110, 10},
		{"contradictory-total-incomplete-cache", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 50, Reported: complete, CacheTokensAdditive: true, InvalidFields: agentruntime.ModelUsageFields{CacheRead: true}}, "estimated", 1010, 10},
		{"contradictory-total-complete-components", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 50, Reported: complete}, "estimated", 110, 10},
		{"valid-cache-sibling-bounds-total", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 20, TotalTokens: 110, Reported: &agentruntime.ModelUsageFields{Input: true, Output: true, Total: true, CacheRead: true}, CacheTokensAdditive: true, InvalidFields: agentruntime.ModelUsageFields{CacheWrite: true}}, "estimated", 1010, 10},
		{"unrepresentable-known-minimum", agentruntime.ModelUsage{InputTokens: int(^uint(0) >> 1), CacheReadTokens: 1, TotalTokens: int(^uint(0) >> 1), Reported: &agentruntime.ModelUsageFields{Input: true, Output: true, Total: true, CacheRead: true}, CacheTokensAdditive: true}, "estimated", 1000, 0},
		{"healed-current-cache-history-remains", agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110, Reported: complete, CacheTokensAdditive: true, Warnings: warnings}, "provider", 110, 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := runnerContextUsage{InputEstimates: []runnerContextUsageRow{{Key: "messages", Tokens: 1000}}}
			response := agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "done"}, Usage: test.usage}
			source, used, output := settleRunnerContextUsage(snapshot, response)
			if source != test.source || used != test.used || output != test.output {
				t.Fatalf("settlement=(%s,%d,%d), want=(%s,%d,%d)", source, used, output, test.source, test.used, test.output)
			}
		})
	}
	for _, explicitTotal := range []bool{false, true} {
		t.Run(map[bool]string{false: "aggregate-mixed-cache-invalid", true: "aggregate-independent-total"}[explicitTotal], func(t *testing.T) {
			left := agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, Reported: components,
				CacheTokensAdditive: true, InvalidFields: agentruntime.ModelUsageFields{CacheRead: true}, Warnings: warnings}
			if explicitTotal {
				left.TotalTokens, left.Reported = 110, complete
			}
			right := agentruntime.ModelUsage{InputTokens: 50, OutputTokens: 5, TotalTokens: 55, Reported: complete}
			combined := addModelUsage(left, right)
			if combined.CacheTokensAdditive || !combined.InvalidFields.Input || !combined.InvalidFields.CacheRead ||
				combined.Warnings != warnings || !combined.ReportedFields().Input || !combined.ReportedFields().Output || combined.ReportedFields().Total != explicitTotal {
				t.Fatalf("distinct-request aggregation lost dependency or AND presence: %+v", combined)
			}
			source, used, output := settleRunnerContextUsage(runnerContextUsage{InputEstimates: []runnerContextUsageRow{{Tokens: 1000}}}, agentruntime.ModelResponse{Usage: combined})
			wantSource, wantUsed := "estimated", 1015
			if explicitTotal {
				wantSource, wantUsed = "provider", 165
			}
			if source != wantSource || used != wantUsed || output != 15 {
				t.Fatalf("aggregate settlement=(%s,%d,%d)", source, used, output)
			}
		})
	}
	t.Run("aggregate-mixed-complete-derived-cache", func(t *testing.T) {
		left := agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 20, Reported: &agentruntime.ModelUsageFields{Input: true, Output: true, CacheRead: true}}
		left.DeriveTotal(true)
		right := agentruntime.ModelUsage{InputTokens: 50, OutputTokens: 5, Reported: components}
		right.DeriveTotal(false)
		combined := addModelUsage(left, right)
		source, used, output := settleRunnerContextUsage(runnerContextUsage{}, agentruntime.ModelResponse{Usage: combined})
		if combined.ReportedFields().Total || combined.CacheTokensAdditive || source != "provider" || used != 185 || output != 15 {
			t.Fatalf("mixed complete-cache settlement=(%s,%d,%d), usage=%+v", source, used, output, combined)
		}
	})
	t.Run("aggregate-overflow", func(t *testing.T) {
		left := agentruntime.ModelUsage{InputTokens: int(^uint(0) >> 1), TotalTokens: int(^uint(0) >> 1), Reported: complete}
		right := agentruntime.ModelUsage{InputTokens: 1, TotalTokens: 1, Reported: complete}
		combined := addModelUsage(left, right)
		if combined.InputTokens != 0 || combined.TotalTokens != 0 || !combined.InvalidFields.Input || !combined.InvalidFields.Total || combined.Warnings.Empty() {
			t.Fatalf("overflow became valid usage: %+v", combined)
		}
		source, used, output := settleRunnerContextUsage(runnerContextUsage{InputEstimates: []runnerContextUsageRow{{Tokens: 1000}}}, agentruntime.ModelResponse{Usage: combined})
		if source != "estimated" || used != 1000 || output != 0 {
			t.Fatalf("overflow settlement=(%s,%d,%d)", source, used, output)
		}
	})
	t.Run("aggregate-additive-input-overflow", func(t *testing.T) {
		for _, side := range []string{"left", "right"} {
			t.Run(side, func(t *testing.T) {
				maximum := int(^uint(0) >> 1)
				request := agentruntime.ModelUsage{
					InputTokens: maximum, CacheReadTokens: 1, CacheTokensAdditive: true,
					Reported: &agentruntime.ModelUsageFields{Input: true, Output: true, CacheRead: true, CacheWrite: true},
				}
				request.DeriveTotal(true)
				if request.TotalTokens != 0 || request.Warnings.Empty() || request.InvalidFields != (agentruntime.ModelUsageFields{}) {
					t.Fatal("fixture must have current valid scalar receipts and an overflowing additive dependency")
				}
				zero := agentruntime.ModelUsage{Reported: complete}
				combine := func(value agentruntime.ModelUsage) agentruntime.ModelUsage {
					if side == "left" {
						return addModelUsage(value, zero)
					}
					return addModelUsage(zero, value)
				}
				check := func(usage agentruntime.ModelUsage, wantSource string, wantUsed int) {
					t.Helper()
					source, used, output := settleRunnerContextUsage(runnerContextUsage{InputEstimates: []runnerContextUsageRow{{Tokens: 1000}}}, agentruntime.ModelResponse{Usage: usage})
					if source != wantSource || used != wantUsed || output != 0 {
						t.Fatalf("additive dependency settlement=(%s,%d,%d), want=(%s,%d,0)", source, used, output, wantSource, wantUsed)
					}
				}
				combined := combine(request)
				if combined.CacheTokensAdditive || !combined.InvalidFields.Input || combined.InvalidFields.Total || combined.Warnings != request.Warnings {
					t.Fatal("mixed aggregation discarded current overflow or changed independent total/warning state")
				}
				check(combined, "estimated", 1000)
				authoritative := request
				authoritative.TotalTokens, authoritative.Reported = maximum, &agentruntime.ModelUsageFields{Input: true, Output: true, Total: true, CacheRead: true, CacheWrite: true}
				check(combine(authoritative), "estimated", 1000)
				// A new valid snapshot changes current cache, not the historical
				// warning. The aggregation must recompute, not retain a synthetic bit.
				healed := request
				healed.CacheReadTokens = 0
				healed.DeriveTotal(true)
				recovered := combine(healed)
				if recovered.InvalidFields.Input || recovered.Warnings != request.Warnings || request.InvalidFields != (agentruntime.ModelUsageFields{}) {
					t.Fatal("valid current snapshot was poisoned by historical warning or request mutation")
				}
				check(recovered, "provider", maximum)
			})
		}
	})
}

type usageWarningAuditModel struct{ usage agentruntime.ModelUsage }

func (model usageWarningAuditModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "done"}, Usage: model.usage}, nil
}

func TestModelUsageWarningAuditPreservation(t *testing.T) {
	known := agentruntime.ModelUsageWarnings(0).Add(agentruntime.UsageFieldOutput, agentruntime.UsageCounterType)
	for _, test := range []struct {
		name     string
		warnings agentruntime.ModelUsageWarnings
	}{
		{"ordinary-success", 0}, {"closed-warning", known}, {"unknown-high-bits", agentruntime.ModelUsageWarnings(1 << 15)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := runtimekv.New(filepath.Join(t.TempDir(), "usage-audit.sqlite"))
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			server := &Server{runtimeStore: store}
			record := providers.AuditRecord{HTTPStatus: 200, Attempt: 1, PromptTokens: 20, TotalTokens: 20, UsageWarnings: test.warnings}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			_, present := wire["usageWarnings"]
			if present == test.warnings.Empty() {
				t.Fatal("audit added empty warnings or dropped valid warnings")
			}
			var restored providers.AuditRecord
			if err := json.Unmarshal(raw, &restored); err != nil || restored.UsageWarnings != test.warnings.Merge(0) || restored.Error != "" {
				t.Fatalf("typed audit round-trip failed: %v", err)
			}
			server.recordSessionRunnerModelAudit("usage-audit-fixture", 1, record)
			server.recordKernelHostModelAudit(workspace.KernelFrameAccess{UserID: "fixture-user", Frame: workspace.Frame{ID: "fixture-frame", RootFrameID: "fixture-frame", ProjectID: "fixture-project"}}, record)
			for _, namespace := range []string{sessionRunnerModelAuditRuntimeNamespace, kernelHostLLMAuditNamespace} {
				entries, err := store.List(namespace)
				if err != nil || len(entries) != 1 {
					t.Fatalf("temporary audit entries=%d err=%v", len(entries), err)
				}
				value, ok := entries[0].Value.(map[string]any)
				if !ok {
					t.Fatal("temporary audit is not a map")
				}
				_, present := value["usageWarnings"]
				if present == test.warnings.Empty() || value["error"] != "" {
					t.Fatal("warning changed primary audit semantics")
				}
				if present {
					actual, err := json.Marshal(value["usageWarnings"])
					want, _ := json.Marshal(test.warnings)
					if err != nil || string(actual) != string(want) {
						t.Fatal("closed warning list was dropped or changed")
					}
				}
			}
			audits := 0
			client := sessionRunnerAuditedStaticModelClient{
				delegate: usageWarningAuditModel{usage: agentruntime.ModelUsage{InputTokens: 20, TotalTokens: 20, Warnings: test.warnings}},
				audit: func(record providers.AuditRecord) {
					audits++
					if record.UsageWarnings != test.warnings || record.Error != "" || record.PromptTokens != 20 {
						t.Error("static adapter lost usage or changed primary success")
					}
				},
			}
			response, err := client.Complete(context.Background(), agentruntime.ModelRequest{})
			if err != nil || response.Message.Content != "done" || audits != 1 {
				t.Fatalf("static result/audit changed: audits=%d err=%v", audits, err)
			}
		})
	}
}
