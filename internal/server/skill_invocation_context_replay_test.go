package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSkillArgumentContextSurvivesCompactionAndReopen(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	disableLoadedClosureFixtureMCP(t, fixture)
	const name, args, callID = "scoped-context-replay-fixture", " scope alpha ", "load-original-scoped-context"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Before<$ARGUMENTS>After")); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	run, gateway := loadedClosureFixtureGateway(fixture)
	original := executeLoadedClosureFixture(t, fixture, run, gateway, callID, name, args)
	full, ok := original.Value.(string)
	const want = "Before< scope alpha >After"
	if !ok || !strings.Contains(full, want) || !original.SkillLoadReceipt.MatchesArguments(name, args, "") {
		t.Fatal("real original load did not render and bind the exact raw invocation arguments")
	}
	var executedInput map[string]any
	if err := json.Unmarshal(original.ExecutedArguments, &executedInput); err != nil || executedInput["args"] != args {
		t.Fatalf("actual admitted invocation arguments were not preserved: %v", err)
	}
	var sourceProven bool
	for _, entry := range readLoadedClosureEntries(t, fixture.repo, fixture.stream) {
		if stringValue(entry.Message["toolCallId"]) != callID || stringValue(entry.Message["toolPhase"]) != "completed" {
			continue
		}
		receipt := runtimeSkillLoadReceiptFromCheckpoint(entry)
		// Equal requested/executed inputs share toolInput in the canonical
		// writer; a distinct admitted input is retained as executedToolInput.
		executed := completedSkillInput(entry.Message)
		if entry.EventID <= 0 || entry.ClientMessageID == "" ||
			!runtimeSkillLoadReceiptForScope(receipt, run.Transcript, callID) ||
			receipt.InvocationKey != original.SkillLoadReceipt.InvocationKey ||
			executed["args"] != args || stringValue(entry.Message["toolResult"]) != full {
			t.Fatal("original canonical completed checkpoint did not retain executed input, full result and bound receipt")
		}
		sourceProven = true
	}
	if !sourceProven {
		t.Fatal("no canonical original full successful Skill checkpoint; this is not a valid restoration fixture")
	}
	entries, compactID := persistLoadedClosureCompaction(t, fixture, run)
	entries, resumedRun, _ := reopenLoadedClosureFixture(t, fixture, entries, compactID)
	// Pin the same frozen canonical projection used by real preparation. The
	// compact entry's digest alone is not original-source visibility authority.
	if _, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), resumedRun.Transcript, 20, 100); err != nil {
		t.Fatal(err)
	}
	receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, resumedRun)
	if err != nil || len(receipts) != 1 || receipts[0].InvocationKey != original.SkillLoadReceipt.InvocationKey {
		t.Fatalf("original durable receipt was lost before context recovery: count=%d err=%v", len(receipts), err)
	}
	_, prepared, keys, changed, err := fixture.server.prepareRuntimeSkillReplayContext(
		context.Background(), selectedLoadedClosureFixture(t, fixture, name), receipts, nil, resumedRun, resumedRun.SessionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := fixture.server.runtimeSkillContextFromSkills(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(prompt, want) != 1 || strings.Contains(prompt, "Before<>After") ||
		strings.Contains(prompt, "$ARGUMENTS") || len(keys) != 1 ||
		keys[0] != original.SkillLoadReceipt.InvocationKey || len(changed) != 0 {
		t.Fatalf("original scoped Skill context was not recovered exactly once: scopedBodies=%d keys=%d changed=%v", strings.Count(prompt, want), len(keys), changed)
	}
	if resumedRun.hasExecutedSkillInvocationKey(original.SkillLoadReceipt.InvocationKey) {
		t.Fatal("preparing a candidate view activated a key before actual provider context inclusion")
	}
	assertOriginalSkillContextAtProvider(t, fixture, resumedRun, name, want, "Before<>After", "$ARGUMENTS")
	if !resumedRun.hasExecutedSkillInvocationKey(original.SkillLoadReceipt.InvocationKey) {
		t.Fatal("the actual included original context did not activate its exact load key")
	}
}
