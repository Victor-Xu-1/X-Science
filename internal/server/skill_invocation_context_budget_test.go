package server

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSkillOriginalContextBudgetRequiresReloadNotFailure(t *testing.T) {
	t.Run("optional-replay-requires-reload", func(t *testing.T) { testSkillOriginalContextBudget(t, false) })
	t.Run("oversized-baseline-remains-error", func(t *testing.T) { testSkillOriginalContextBudget(t, true) })
}

func testSkillOriginalContextBudget(t *testing.T, oversizedBaseline bool) {
	t.Helper()
	fixture := newKernelSkillDraftFixture(t)
	disableLoadedClosureFixtureMCP(t, fixture)
	names := []string{"context-budget-first-fixture", "context-budget-second-fixture"}
	for _, name := range names {
		if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Before<$ARGUMENTS>After")); err != nil {
			t.Fatal(err)
		}
		publishLoadedClosureFixture(t, fixture, name, false)
	}
	originalNames := append([]string(nil), names...)
	if oversizedBaseline {
		for _, name := range []string{"unloaded-budget-first-fixture", "unloaded-budget-second-fixture"} {
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, strings.Repeat("y", maxRuntimeSkillContractBytes*3/5))); err != nil {
				t.Fatal(err)
			}
			publishLoadedClosureFixture(t, fixture, name, false)
			names = append(names, name)
		}
	}
	run, gateway := loadedClosureFixtureGateway(fixture)
	arguments := strings.Repeat("x", maxRuntimeSkillContractBytes*3/5)
	for _, name := range originalNames {
		loaded := executeLoadedClosureFixture(t, fixture, run, gateway, "load-"+name, name, arguments)
		if !loaded.SkillLoadReceipt.Valid() || !strings.Contains(loaded.Value.(string), "Before<"+arguments+">After") {
			t.Fatal("each original invocation must independently fit and retain its real full checkpoint")
		}
	}
	entries, compactID := persistLoadedClosureCompaction(t, fixture, run)
	entries, resumed, _ := reopenLoadedClosureFixture(t, fixture, entries, compactID)
	if _, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), resumed.Transcript, 20, 100); err != nil {
		t.Fatal(err)
	}
	receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, resumed)
	if err != nil || len(receipts) != len(originalNames) {
		t.Fatalf("the original receipts must survive independently of optional restoration: count=%d err=%v", len(receipts), err)
	}
	_, prepared, keys, changed, err := fixture.server.prepareRuntimeSkillReplayContext(context.Background(),
		selectedLoadedClosureFixture(t, fixture, names...), receipts, nil, resumed, resumed.SessionID)
	if oversizedBaseline {
		if !errors.Is(err, errRuntimeSkillContextBudget) || len(keys) != 0 {
			t.Fatalf("an already oversized selected baseline was silently accepted: err=%v keys=%d", err, len(keys))
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.runtimeSkillContextFromSkills(prepared); err != nil {
		t.Fatalf("optional restored content exhausted the combined budget instead of requiring reload: %v", err)
	}
	if len(keys) != 0 || len(prepared) != 0 || strings.Join(changed, ",") != strings.Join(names, ",") {
		t.Fatalf("over-budget replay granted partial context or keys: skills=%d keys=%d changed=%v", len(prepared), len(keys), changed)
	}
}
