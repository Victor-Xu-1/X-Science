package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"synon-go/internal/agentruntime"
	"synon-go/internal/skills"
)

func TestSkillModelProjectionPreservesLoadOnlyReceipt(t *testing.T) {
	for _, description := range []string{"", strings.Repeat("Detailed capability description. ", 100)} {
		payload := map[string]any{
			"skill":  map[string]any{"name": "analysis-workflow", "description": description},
			"prompt": "Use the available execution tool with the loaded contract.",
			"data":   map[string]any{"loaded": true, "executed": false},
		}
		projected, ok := agentRuntimeSkillModelResult(payload).(string)
		if !ok {
			t.Fatalf("projection type=%T", agentRuntimeSkillModelResult(payload))
		}
		header := strings.SplitN(projected, "\n", 2)[0]
		if !strings.Contains(header, `loaded="true" executed="false"`) {
			t.Fatalf("model-visible receipt lost load/execution distinction: %q", header)
		}
		if name, found := providerSkillResultName(projected); !found || name != "analysis-workflow" {
			t.Fatalf("receipt identity=%q found=%t", name, found)
		}
		legacy := agentRuntimeLegacySkillModelResult(projected).(string)
		if !strings.Contains(legacy, `loaded="true" executed="false"`) || strings.Contains(legacy, payload["prompt"].(string)) {
			t.Fatalf("replay lost receipt or retained stale contract: %q", legacy)
		}
	}
}

func TestSkillModelProjectionDoesNotInventReceiptFields(t *testing.T) {
	for _, data := range []map[string]any{nil, {"loaded": "true", "executed": "false"}} {
		projected := agentRuntimeSkillModelResult(map[string]any{
			"skill": map[string]any{"name": "analysis-workflow"}, "prompt": "Contract", "data": data,
		}).(string)
		if strings.Contains(projected, `loaded=`) || strings.Contains(projected, `executed=`) {
			t.Fatalf("untyped or absent facts became authoritative: %q", projected)
		}
	}
	for _, header := range []string{
		`<skill-metadata name="analysis-workflow"junk />`,
		`<skill-metadata name="analysis-workflow" loaded="true"`,
		`<skill-metadata name=analysis-workflow />`,
	} {
		if name, found := providerSkillResultName(header); found {
			t.Fatalf("malformed receipt accepted as %q: %q", name, header)
		}
	}
}

func TestSkillLoadReceiptSurvivesRealLargeResultStorage(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	ctx, _ := appendLargeToolResultSource(t, fixture, "load-large-contract", "skill")
	fixture.server.skillCatalog = skills.NewCatalog()
	fixture.server.skillCatalog.AddSkill(skills.Skill{
		Name: "analysis-workflow", Path: "builtin:analysis-workflow",
		Description: "Load an analysis contract", Body: strings.Repeat("Contract detail.\n", 1400),
	})
	payload, err := fixture.server.executeSkillToolWithRuntimeSkillsAndPolicy(
		ctx, map[string]any{"skill": "analysis-workflow"}, nil, runtimeSkillPolicyAuthority{}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	data := mapValue(mapValue(payload)["data"])
	if data["loaded"] != true || data["executed"] != false {
		t.Fatalf("loader did not produce the expected receipt: %#v", data)
	}
	projected := agentRuntimeSkillModelResult(payload)
	raw, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	engine := fixture.server.newAgentRuntimeEngineWithContext(ctx, SessionRunnerChatOptions{
		SessionID: fixture.stream.SessionID, OutputLimitBytes: 50_000,
	})
	call := agentruntime.ToolCall{ID: "load-large-contract", Name: "skill"}
	materialized, err := engine.MaterializeToolResult(ctx, call, projected, agentruntime.ToolResultSucceeded)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor agentruntime.LargeToolResultDescriptor
	if err := json.Unmarshal(materialized.JSON, &descriptor); err != nil {
		t.Fatal(err)
	}
	if materialized.ResultRef == "" || !descriptor.Truncated {
		t.Fatalf("large fixture did not use immutable storage: %#v", descriptor)
	}
	// Preview JSON may escape the textual metadata, but both exact receipt
	// fields must remain in its bounded leading window, not only a load success.
	preview := strings.ReplaceAll(descriptor.Preview, `\`, "")
	if !strings.Contains(preview, `loaded="true" executed="false"`) {
		t.Fatalf("externalization hid the load-only receipt: %s", descriptor.Preview)
	}
	if !bytes.HasPrefix(raw, []byte(descriptor.Preview)) {
		if err := (runnerLargeToolResultAuthority{}).ValidatePreview(ctx, agentruntime.LargeToolResultInput{
			ToolCall: call, RawJSON: raw, Outcome: agentruntime.ToolResultSucceeded, MaxInlineBytes: engine.MaxToolResultBytes,
		}, descriptor); err != nil {
			t.Fatal(err)
		}
	}
	assertLargeResultExactContentHTTP(t, fixture.server.Handler(), fixture.stream.OwnerID, descriptor, raw)
	replayed, err := compactRunnerLargeToolResultDescriptorForReplay(string(materialized.JSON))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replayed, "loaded") || !strings.Contains(replayed, "executed") {
		t.Fatalf("replay lost load-only receipt: %s", replayed)
	}
}
