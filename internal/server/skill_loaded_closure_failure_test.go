package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/skills"
)

func TestSkillLoadReceiptRequiresSuccessfulDurableTerminal(t *testing.T) {
	for _, terminal := range []string{"completed", "cancelled", "stale", "failed", "rejected", "wrong-args", "wrong-filter"} {
		t.Run(terminal, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name, callID = "durable-load-fixture", "load-before-terminal"
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Load a bounded contract.")); err != nil {
				t.Fatal(err)
			}
			publishLoadedClosureFixture(t, fixture, name, false)
			run, gateway := loadedClosureFixtureGateway(fixture)
			ctx := withTranscriptRunnerChatRun(context.Background(), run)
			arguments := json.RawMessage(`{"skill":"durable-load-fixture"}`)
			result, err := gateway.Execute(ctx, agentruntime.ToolCall{ID: callID, Name: "skill", Arguments: arguments})
			if err != nil || !result.SkillLoadReceipt.BoundToCall(callID) {
				t.Fatalf("host load receipt=%v err=%v", result.SkillLoadReceipt != nil, err)
			}
			if run.hasExecutedSkillInvocationKey(result.SkillLoadReceipt.InvocationKey) {
				t.Fatal("gateway success activated exact reuse before durable completion")
			}
			options := SessionRunnerChatOptions{RunnerID: run.Transcript.Claim.RunnerID, SessionID: run.SessionID}
			switch terminal {
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				err = completeLoadedClosureFixture(cancelled, fixture, options, run, callID, arguments, result)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled completion: %v", err)
				}
			case "stale":
				if _, err := fixture.repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{
					Claim: fixture.claim, ClientMessageID: "invalidate-load-owner", ReasonCode: "runtime_draining", Resumable: true,
				}); err != nil {
					t.Fatal(err)
				}
				if err = completeLoadedClosureFixture(ctx, fixture, options, run, callID, arguments, result); err == nil {
					t.Fatal("stale completion was accepted")
				}
			case "failed", "rejected":
				event := agentruntime.Event{
					Type: agentruntime.EventToolFailed, ToolName: "skill", ToolCallID: callID, Arguments: string(arguments),
					Result: `{"ok":false,"error":"controlled terminal failure"}`, SkillLoadReceipt: result.SkillLoadReceipt,
				}
				if terminal == "rejected" {
					event.Type, event.RejectedBeforeExecution = agentruntime.EventToolCompleted, true
				}
				if err := fixture.server.checkpointSessionRunnerToolEvent(ctx, options, run, event); err != nil {
					t.Fatal(err)
				}
			case "wrong-args", "wrong-filter":
				input := map[string]any{"skill": name}
				if terminal == "wrong-args" {
					input["args"] = "a different executed scope"
				} else {
					input["filter"] = "a different executed filter"
				}
				result.ExecutedArguments, err = json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				if err := completeLoadedClosureFixture(ctx, fixture, options, run, callID, arguments, result); err != nil {
					t.Fatal(err)
				}
			default:
				if err := completeLoadedClosureFixture(ctx, fixture, options, run, callID, arguments, result); err != nil {
					t.Fatal(err)
				}
			}
			if actual := run.hasExecutedSkillInvocationKey(result.SkillLoadReceipt.InvocationKey); actual != (terminal == "completed") {
				t.Fatalf("terminal %s established exact reuse=%v", terminal, actual)
			}
			entries := readLoadedClosureEntries(t, fixture.repo, fixture.stream)
			receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, run)
			if err != nil || (len(receipts) != 0) != (terminal == "completed") {
				t.Fatalf("terminal %s durable receipts=%d err=%v", terminal, len(receipts), err)
			}
		})
	}
}

func TestSkillLoadReplayRejectsInputOnlyOrTransplantedReceipt(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name, callID = "scoped-load-fixture", "load-scoped-contract"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Scoped contract.")); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	run, gateway := loadedClosureFixtureGateway(fixture)
	loaded := executeLoadedClosureFixture(t, fixture, run, gateway, callID, name, "scope")
	for _, mutation := range []string{"input-only", "wrong-call", "wrong-owner", "wrong-stream", "wrong-attempt", "wrong-input", "changed-digest", "wrong-args", "wrong-filter"} {
		t.Run(mutation, func(t *testing.T) {
			entries := readLoadedClosureEntries(t, fixture.repo, fixture.stream)
			for index := range entries {
				message := entries[index].Message
				if stringValue(message["toolCallId"]) != callID || stringValue(message["toolPhase"]) != "completed" {
					continue
				}
				receipt := loaded.SkillLoadReceipt.Clone()
				switch mutation {
				case "input-only":
					delete(message, runtimeSkillLoadReceiptField)
					input := mapValue(message["toolInput"])
					input[runtimeSkillLoadReceiptField] = receipt
					message["toolInput"] = input
				case "wrong-call":
					receipt.Binding.ToolCallID = "another-load"
				case "wrong-owner":
					receipt.Binding.OwnerID = "another-owner"
				case "wrong-stream":
					receipt.Binding.StreamUID = "another-stream"
				case "wrong-attempt":
					receipt.Binding.RunnerAttempt++
				case "wrong-input":
					receipt.Binding.InputRevision++
				case "changed-digest":
					receipt.Contracts[0].BundleSHA256 = strings.Repeat("f", 64)
				case "wrong-args", "wrong-filter":
					input := mapValue(message["toolInput"])
					if mutation == "wrong-args" {
						input["args"] = "not the executed scope"
					} else {
						input["filter"] = "not the executed filter"
					}
					message["executedToolInput"] = input
				}
				if mutation != "input-only" {
					message[runtimeSkillLoadReceiptField] = receipt
				}
			}
			if receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, run); err != nil || len(receipts) != 0 {
				t.Fatalf("transplanted receipt %s was accepted: receipts=%d err=%v", mutation, len(receipts), err)
			}
		})
	}
}

func TestSkillLoadReceiptUsesExecutedArgumentsAndIgnoresCheckpointConflict(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name, callID = "conflict-load-fixture", "same-checkpoint-call"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "First contract.")); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	run, gateway := loadedClosureFixtureGateway(fixture)
	first := executeLoadedClosureFixture(t, fixture, run, gateway, callID, name, "scope")
	entries := readLoadedClosureEntries(t, fixture.repo, fixture.stream)
	for index := range entries {
		message := entries[index].Message
		if stringValue(message["toolCallId"]) == callID && stringValue(message["toolPhase"]) == "completed" {
			message["executedToolInput"] = completedSkillInput(message)
			message["toolInput"] = map[string]any{"skill": name, "args": "requested but not executed"}
		}
	}
	if receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, run); err != nil || len(receipts) != 1 {
		t.Fatalf("requested input overrode actual execution: %d %v", len(receipts), err)
	}
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Second contract.", "First contract."); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, true)
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	arguments := json.RawMessage(`{"skill":"conflict-load-fixture","args":"scope"}`)
	revised, err := gateway.Execute(ctx, agentruntime.ToolCall{ID: callID, Name: "skill", Arguments: arguments})
	if err != nil || revised.SkillLoadReceipt == nil || revised.SkillLoadReceipt.InvocationKey == first.SkillLoadReceipt.InvocationKey {
		t.Fatalf("prepare distinct real host receipt: %v", err)
	}
	if err := completeLoadedClosureFixture(ctx, fixture, SessionRunnerChatOptions{RunnerID: run.Transcript.Claim.RunnerID}, run, callID, arguments, revised); err != nil {
		t.Fatalf("existing global conflict tolerance changed: %v", err)
	}
	if run.hasExecutedSkillInvocationKey(revised.SkillLoadReceipt.InvocationKey) {
		t.Fatal("a tolerated divergent checkpoint conflict granted exact load reuse")
	}
	if !run.hasExecutedSkillInvocationKey(first.SkillLoadReceipt.InvocationKey) {
		t.Fatal("conflict erased the original durable load fact")
	}
}

func TestSkillLoadCompactionCannotTransferReceiptAcrossLogicalInputs(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "input-bound-load-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Input scoped instructions.")); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	run, gateway := loadedClosureFixtureGateway(fixture)
	executeLoadedClosureFixture(t, fixture, run, gateway, "load-old-input", name, "scope")
	old, err := sessionRunnerToolContinuityRecords(readLoadedClosureEntries(t, fixture.repo, fixture.stream))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: fixture.claim, ClientMessageID: "end-old-skill-input", ReasonCode: "runtime_draining", Resumable: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.repo.AppendUserEvent(context.Background(), transcriptstore.AppendUserEventInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
		ClientMessageID: "new-skill-input", PayloadJSON: []byte(`{"text":"Start an independent bounded task."}`),
	}); err != nil {
		t.Fatal(err)
	}
	next, err := fixture.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: fixture.claim.RunnerID,
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !next.Claimed || next.Claim.ClaimedInputRevision == fixture.claim.ClaimedInputRevision {
		t.Fatalf("real next input claim: %v %v", next.Claimed, err)
	}
	run.Transcript.Claim = next.Claim
	run.Attempt, run.ClaimToken = int(next.Claim.Attempt), next.Claim.ClaimToken
	if err := fixture.server.checkpointChatTool(SessionRunnerChatOptions{RunnerID: next.Claim.RunnerID}, run,
		"running", "Retain historical continuity.", "", "auto_compact", map[string]any{sessionRunnerToolContinuityField: old}); err != nil {
		t.Fatal(err)
	}
	entries, err := fixture.server.runnerEntriesForCurrentLogicalInput(context.Background(), readLoadedClosureEntries(t, fixture.repo, fixture.stream), run)
	if err != nil {
		t.Fatal(err)
	}
	if receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, run); err != nil || len(receipts) != 0 {
		t.Fatalf("current-input compact checkpoint authorized an old-input receipt: %d %v", len(receipts), err)
	}
}

func TestSkillLoadUnknownLegacyPackageDoesNotInventBundleIdentity(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	for _, path := range []string{"", filepath.Join(t.TempDir(), "missing", "SKILL.md")} {
		snapshot, err := fixture.server.captureRuntimeSkillClosureSnapshot([]skills.Skill{{Name: "legacy-declarative", Path: path, Body: "Retain legacy instructions."}})
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := snapshot.receipt("legacy-declarative", "", "")
		if err != nil || receipt != nil || snapshot.contracts[0].bundle.digest != "" {
			t.Fatal("missing legacy package acquired a fabricated full-bundle identity")
		}
		prepared, err := snapshot.materialize(context.Background(), fixture.server, fixture.stream.SessionID)
		if err != nil || len(prepared) != 1 || prepared[0].Body != "Retain legacy instructions." {
			t.Fatal("unknown-version compatibility became a new Skill load gate")
		}
	}
	regular := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(regular, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.captureRuntimeSkillClosureSnapshot([]skills.Skill{{Name: "io-failure", Path: filepath.Join(regular, "SKILL.md"), Body: "Instructions."}}); err == nil {
		t.Fatal("unexpected source I/O error was hidden as a virtual package")
	}
}

func TestSkillClosureIdentityUsesEffectiveContractInsteadOfCachedBodyHash(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	skill := skills.Skill{Name: "inline-contract-fixture", Path: "builtin:inline-contract-fixture", Body: "Effective contract."}
	first, err := fixture.server.captureRuntimeSkillClosureSnapshot([]skills.Skill{skill})
	if err != nil {
		t.Fatal(err)
	}
	skill.BodyHash = "untrusted-or-stale-cache-value"
	second, err := fixture.server.captureRuntimeSkillClosureSnapshot([]skills.Skill{skill})
	if err != nil || first.contracts[0].identity() != second.contracts[0].identity() {
		t.Fatal("a cached BodyHash changed identity without changing the effective contract")
	}
	skill.CriticalConstraints = []string{"Retain an added general constraint."}
	third, err := fixture.server.captureRuntimeSkillClosureSnapshot([]skills.Skill{skill})
	if err != nil || first.contracts[0].contractSHA256 == third.contracts[0].contractSHA256 {
		t.Fatal("effective non-body contract fields were omitted from load identity")
	}
}
