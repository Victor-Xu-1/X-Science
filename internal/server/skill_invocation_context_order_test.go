package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/skills"
)

func TestSkillOriginalContextFollowsLatestAcknowledgement(t *testing.T) {
	for _, scenario := range []string{"latest-full", "latest-compact-earlier-variant", "unproven-success", "failed-later-call", "full-source-outside-replay-window"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			disableLoadedClosureFixtureMCP(t, fixture)
			const name = "ordered-original-context-fixture"
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Before<$ARGUMENTS>After")); err != nil {
				t.Fatal(err)
			}
			publishLoadedClosureFixture(t, fixture, name, false)
			run, gateway := loadedClosureFixtureGateway(fixture)
			first := executeLoadedClosureFixture(t, fixture, run, gateway, "load-first", name, "first scope")
			want, key := "Before<first scope>After", first.SkillLoadReceipt.InvocationKey
			switch scenario {
			case "latest-full", "latest-compact-earlier-variant":
				second := executeLoadedClosureFixture(t, fixture, run, gateway, "load-second", name, "second scope")
				if scenario == "latest-full" {
					want, key = "Before<second scope>After", second.SkillLoadReceipt.InvocationKey
				} else {
					repeated := executeLoadedClosureFixture(t, fixture, run, gateway, "ack-first-again", name, "first scope")
					if mapValue(repeated.Value)["reused"] != true {
						t.Fatal("fixture did not produce a real compact acknowledgement")
					}
				}
			case "unproven-success", "failed-later-call":
				arguments, err := json.Marshal(map[string]any{"skill": name, "args": "unproven later scope"})
				if err != nil {
					t.Fatal(err)
				}
				options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: run.Transcript.Claim.RunnerID}
				ctx := withTranscriptRunnerChatRun(context.Background(), run)
				if err := fixture.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
					Type: agentruntime.EventToolStarted, ToolName: "skill", ToolCallID: "later-unproven", Arguments: string(arguments),
				}); err != nil {
					t.Fatal(err)
				}
				event := agentruntime.Event{Type: agentruntime.EventToolCompleted, ToolName: "skill", ToolCallID: "later-unproven",
					Arguments: string(arguments), ExecutedArguments: string(arguments), Result: `"A later result with no host load receipt."`}
				if scenario == "unproven-success" {
					want, key = "", ""
				} else {
					event.Type, event.Result = agentruntime.EventToolFailed, `{"success":false,"error":"synthetic failed invocation"}`
				}
				if err := fixture.server.checkpointSessionRunnerToolEvent(ctx, options, run, event); err != nil {
					t.Fatal(err)
				}
			case "full-source-outside-replay-window":
				// This exercises the real32-checkpoint retention boundary, not
				// artificial elapsed time or a scientific task endurance claim.
				for index := 0; index < 35; index++ {
					repeated := executeLoadedClosureFixture(t, fixture, run, gateway, fmt.Sprintf("ack-%d", index), name, "first scope")
					if mapValue(repeated.Value)["reused"] != true {
						t.Fatal("fixture did not produce the real compact repetition")
					}
				}
			}
			prepared, keys, changed, _, _ := reopenOriginalSkillContextFixture(t, fixture, run, name)
			prompt, err := fixture.server.runtimeSkillContextFromSkills(prepared)
			if err != nil {
				t.Fatal(err)
			}
			if key == "" {
				if len(keys) != 0 || len(prepared) != 0 || strings.Join(changed, ",") != name {
					t.Fatal("later unproven success resurrected an older loaded context/key")
				}
				return
			}
			if len(keys) != 1 || keys[0] != key || len(changed) != 0 || strings.Count(prompt, want) != 1 {
				t.Fatalf("latest canonical acknowledgement did not select one actual original context: keys=%d changed=%v bodies=%d", len(keys), changed, strings.Count(prompt, want))
			}
			for _, competing := range []string{"Before<>After", "Before<first scope>After", "Before<second scope>After"} {
				if competing != want && strings.Contains(prompt, competing) {
					t.Fatal("recovery injected a competing historical invocation body")
				}
			}
		})
	}
}

func reopenOriginalSkillContextFixture(t *testing.T, fixture *kernelSkillDraftFixture, run *sessionRunnerChatRun, names ...string) ([]skills.Skill, []string, []string, *sessionRunnerChatRun, serverAgentRuntimeToolGateway) {
	t.Helper()
	entries, compactID := persistLoadedClosureCompaction(t, fixture, run)
	entries, resumed, gateway := reopenLoadedClosureFixture(t, fixture, entries, compactID)
	if _, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), resumed.Transcript, 20, 100); err != nil {
		t.Fatal(err)
	}
	receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, resumed)
	if err != nil {
		t.Fatal(err)
	}
	_, prepared, keys, changed, err := fixture.server.prepareRuntimeSkillReplayContext(context.Background(),
		selectedLoadedClosureFixture(t, fixture, names...), receipts, nil, resumed, resumed.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return prepared, keys, changed, resumed, gateway
}
