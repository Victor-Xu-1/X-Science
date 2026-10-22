package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func TestSkillLoadParameterizedReplayRequiresExplicitReloadEvenAfterEarlierEmptyLoad(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name, args, filter = "parameterized-replay-fixture", "bounded invocation scope", "selected subset"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Actual scoped contract.")); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	run, gateway := loadedClosureFixtureGateway(fixture)
	empty := executeLoadedClosureFixture(t, fixture, run, gateway, "load-earlier-empty-scope", name, "")
	parameterized := executeLoadedClosureFixture(t, fixture, run, gateway, "load-later-parameterized-scope", name, args, filter)
	if !empty.SkillLoadReceipt.MatchesArguments(name, "", "") || !parameterized.SkillLoadReceipt.MatchesArguments(name, args, filter) {
		t.Fatal("real host fixture did not produce both empty and parameterized invocation identities")
	}
	entries, compactID := persistLoadedClosureCompaction(t, fixture, run)
	entries, resumedRun, resumedGateway := reopenLoadedClosureFixture(t, fixture, entries, compactID)
	receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, resumedRun)
	if err != nil || len(receipts) != 2 {
		t.Fatalf("historical invocation receipts were lost: %d %v", len(receipts), err)
	}
	filtered, keys, unavailable := selectedLoadedClosureFixture(t, fixture, name).forReplay(receipts, nil)
	if len(filtered.contracts) != 0 || len(keys) != 0 || strings.Join(unavailable, ",") != name {
		t.Fatal("earlier empty-scope receipt authorized a reconstructed empty context for a parameterized root")
	}
	resumedRun.addExecutedSkillInvocationKeys(keys...)
	current := executeLoadedClosureFixture(t, fixture, resumedRun, resumedGateway, "reload-real-invocation-scope", name, args, filter)
	if _, full := current.Value.(string); !full || !current.SkillLoadReceipt.MatchesArguments(name, args, filter) || mapValue(current.Value)["reused"] == true {
		t.Fatal("normal explicit load did not restore the actual requested invocation contract")
	}
	if !current.SkillLoadReceipt.BoundToCall("reload-real-invocation-scope") {
		t.Fatal("explicit reload lost the existing host completion binding")
	}
}

func TestSkillLoadReceiptReopensAfterCompactionWithoutSubstitutingPackages(t *testing.T) {
	for _, change := range []string{"unchanged", "leaf", "dependency", "resource"} {
		t.Run(change, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const dependency, name = "replay-dependency-fixture", "replay-leaf-fixture"
			for _, item := range []struct{ name, manifest string }{
				{dependency, kernelSkillManifestForTest(dependency, "Original replay dependency.")},
				{name, loadedClosureDependencyManifest(name, "Original replay leaf.", dependency)},
			} {
				if _, err := fixture.call("host.skills.edit", item.name, "SKILL.md", item.manifest); err != nil {
					t.Fatal(err)
				}
				if item.name == name {
					if _, err := fixture.call("host.skills.edit", name, "references/recipe.txt", "Original replay resource."); err != nil {
						t.Fatal(err)
					}
				}
				publishLoadedClosureFixture(t, fixture, item.name, false)
			}
			run, gateway := loadedClosureFixtureGateway(fixture)
			first := executeLoadedClosureFixture(t, fixture, run, gateway, "load-before-reopen", name, "")
			assertLoadedClosureOrder(t, first.SkillLoadReceipt, dependency, name)
			switch change {
			case "leaf":
				if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Revised replay leaf.", "Original replay leaf."); err != nil {
					t.Fatal(err)
				}
				publishLoadedClosureFixture(t, fixture, name, true)
			case "dependency":
				if _, err := fixture.call("host.skills.edit", dependency, "SKILL.md", "Revised replay dependency.", "Original replay dependency."); err != nil {
					t.Fatal(err)
				}
				publishLoadedClosureFixture(t, fixture, dependency, true)
			case "resource":
				if _, err := fixture.call("host.skills.edit", name, "references/recipe.txt", "Revised replay resource.", "Original replay resource."); err != nil {
					t.Fatal(err)
				}
				publishLoadedClosureFixture(t, fixture, name, true)
			}
			entries, compactID := persistLoadedClosureCompaction(t, fixture, run)
			entries, resumedRun, resumedGateway := reopenLoadedClosureFixture(t, fixture, entries, compactID)
			receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, resumedRun)
			if err != nil {
				t.Fatal(err)
			}
			if len(receipts) != 1 || receipts[0].InvocationKey != first.SkillLoadReceipt.InvocationKey {
				t.Fatal("reopened compacted receipt lost the actually loaded contract")
			}
			captured := selectedLoadedClosureFixture(t, fixture, name)
			replayed, keys, changed := captured.forReplay(receipts, nil)
			resumedRun.addExecutedSkillInvocationKeys(keys...)
			prepared, err := replayed.materialize(context.Background(), fixture.server, resumedRun.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := renderComposedSkillPrompt(prepared, name, "")
			if err != nil {
				t.Fatal(err)
			}
			if change == "unchanged" {
				if !strings.Contains(prompt, "Original replay leaf.") || !strings.Contains(prompt, "Original replay dependency.") ||
					len(changed) != 0 || !resumedRun.hasExecutedSkillInvocationKey(first.SkillLoadReceipt.InvocationKey) {
					t.Fatal("exact unchanged receipt did not restore its actual complete contract")
				}
				reused := executeLoadedClosureFixture(t, fixture, resumedRun, resumedGateway, "load-exact-reopened", name, "")
				if mapValue(reused.Value)["reused"] != true {
					t.Fatal("unchanged reopened contract was unnecessarily reloaded")
				}
				return
			}
			if prompt != "" || len(keys) != 0 || strings.Join(changed, ",") != name {
				t.Fatalf("changed package was silently substituted into an old loaded contract: prompt=%q keys=%d changed=%v", prompt, len(keys), changed)
			}
			current := executeLoadedClosureFixture(t, fixture, resumedRun, resumedGateway, "load-current-after-reopen", name, "")
			if _, ok := current.Value.(string); !ok || current.SkillLoadReceipt.InvocationKey == first.SkillLoadReceipt.InvocationKey {
				t.Fatal("explicit reload did not establish the actual current contract")
			}
			if change == "resource" && current.SkillLoadReceipt.Contracts[1].BundleSHA256 == first.SkillLoadReceipt.Contracts[1].BundleSHA256 {
				t.Fatal("reopened resource change was not bound to the new bytes")
			}
		})
	}
}

func TestSkillLoadReplayKeepsMatchingNewRevisionAndIndependentClosure(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name, independent = "two-revisions-fixture", "independent-load-fixture"
	for skillName, body := range map[string]string{name: "First revision.", independent: "Independent instructions."} {
		if _, err := fixture.call("host.skills.edit", skillName, "SKILL.md", kernelSkillManifestForTest(skillName, body)); err != nil {
			t.Fatal(err)
		}
		publishLoadedClosureFixture(t, fixture, skillName, false)
	}
	run, gateway := loadedClosureFixtureGateway(fixture)
	old := executeLoadedClosureFixture(t, fixture, run, gateway, "load-first-revision", name, "")
	other := executeLoadedClosureFixture(t, fixture, run, gateway, "load-independent", independent, "")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Second revision.", "First revision."); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, true)
	current := executeLoadedClosureFixture(t, fixture, run, gateway, "load-second-revision", name, "")
	entries, compactID := persistLoadedClosureCompaction(t, fixture, run)
	entries, resumedRun, _ := reopenLoadedClosureFixture(t, fixture, entries, compactID)
	receipts, err := fixture.server.restoreSkillLoadReceipts(context.Background(), entries, resumedRun)
	if err != nil {
		t.Fatal(err)
	}
	filtered, keys, changed := selectedLoadedClosureFixture(t, fixture, name, independent).forReplay(receipts, nil)
	resumedRun.addExecutedSkillInvocationKeys(keys...)
	prompt, err := renderComposedSkillPrompt(filtered.skills(), name, "")
	if err != nil || !strings.Contains(prompt, "Second revision.") || !strings.Contains(prompt, "Independent instructions.") ||
		strings.Contains(prompt, "First revision.") || len(changed) != 0 {
		t.Fatalf("older mismatched receipt removed a matching active closure: %v", err)
	}
	if resumedRun.hasExecutedSkillInvocationKey(old.SkillLoadReceipt.InvocationKey) ||
		!resumedRun.hasExecutedSkillInvocationKey(current.SkillLoadReceipt.InvocationKey) ||
		!resumedRun.hasExecutedSkillInvocationKey(other.SkillLoadReceipt.InvocationKey) {
		t.Fatal("recovery selected obsolete revision keys or lost independent exact keys")
	}
}

func TestSkillClosureSnapshotRetainsCapturedBytesAfterPublication(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "captured-bytes-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Original contract uses ${X_SCIENCE_SKILL_DIR}/references/recipe.txt.")); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/recipe.txt", "ORIGINAL_BYTES"); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	snapshot := selectedLoadedClosureFixture(t, fixture, name)
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Revised contract", "Original contract"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/recipe.txt", "REVISED_BYTES", "ORIGINAL_BYTES"); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, true)
	contract := snapshot.contracts[0]
	directory, err := materializeAgentSkillRuntimeBundle(t.TempDir(), name, contract.bundle)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(directory, "references", "recipe.txt"))
	if err != nil || string(bytes) != "ORIGINAL_BYTES" {
		t.Fatalf("captured snapshot read replacement resource bytes: %v", err)
	}
	contract.skill.Path = filepath.Join(directory, "SKILL.md")
	prompt := renderSkillContract(contract.skill, "")
	if !strings.Contains(prompt, "Original contract") || strings.Contains(prompt, "Revised contract") {
		t.Fatal("materialized snapshot rendered a different instruction revision")
	}
	if current := selectedLoadedClosureFixture(t, fixture, name); current.contracts[0].identity() == contract.identity() {
		t.Fatal("actual replacement was not a distinct package identity")
	}
}

func persistLoadedClosureCompaction(t *testing.T, fixture *kernelSkillDraftFixture, run *sessionRunnerChatRun) ([]eventjournal.Entry, int64) {
	t.Helper()
	entries := readLoadedClosureEntries(t, fixture.repo, fixture.stream)
	continuity, err := sessionRunnerToolContinuityRecords(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.checkpointChatTool(SessionRunnerChatOptions{RunnerID: run.Transcript.Claim.RunnerID}, run,
		"running", "Retain bounded tool continuity.", "", "auto_compact", map[string]any{sessionRunnerToolContinuityField: continuity}); err != nil {
		t.Fatal(err)
	}
	entries = readLoadedClosureEntries(t, fixture.repo, fixture.stream)
	for index := len(entries) - 1; index >= 0; index-- {
		if stringValue(entries[index].Message["toolPhase"]) == "auto_compact" {
			return entries, entries[index].EventID
		}
	}
	t.Fatal("real durable compaction checkpoint was not found")
	return nil, 0
}

func reopenLoadedClosureFixture(t *testing.T, fixture *kernelSkillDraftFixture, _ []eventjournal.Entry, compactID int64) ([]eventjournal.Entry, *sessionRunnerChatRun, serverAgentRuntimeToolGateway) {
	t.Helper()
	interrupted, err := fixture.repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: fixture.claim, ClientMessageID: "reopen-skill-receipts", ReasonCode: "runtime_draining", Resumable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := workspace.Open(fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: fixture.claim.RunnerID,
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("reclaim durable Skill input: claimed=%v err=%v", claimed.Claimed, err)
	}
	run, gateway := loadedClosureFixtureGateway(fixture)
	run.Transcript.Claim = claimed.Claim
	run.Attempt, run.ClaimToken = int(claimed.Claim.Attempt), claimed.Claim.ClaimToken
	all := readLoadedClosureEntries(t, repo, fixture.stream)
	var compacted []eventjournal.Entry
	for _, entry := range all {
		if entry.EventID >= compactID {
			compacted = append(compacted, entry)
		}
	}
	scoped, err := fixture.server.runnerEntriesForCurrentLogicalInput(context.Background(), compacted, run)
	if err != nil {
		t.Fatal(err)
	}
	return scoped, run, gateway
}

func readLoadedClosureEntries(t *testing.T, repo *transcriptstore.Repository, stream transcriptstore.Stream) []eventjournal.Entry {
	t.Helper()
	snapshot, err := repo.GetProjectionSnapshot(context.Background(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := repo.ListProjectedEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
		ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]eventjournal.Entry, 0, len(projected))
	for _, event := range projected {
		var message eventjournal.Message
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &message); err != nil {
			t.Fatal(err)
		}
		message["type"] = event.Event.Type
		if event.Event.RunnerAttempt != nil {
			message["runnerAttempt"] = *event.Event.RunnerAttempt
		}
		entries = append(entries, eventjournal.Entry{EventID: event.Event.EventID, ClientMessageID: event.Event.ClientMessageID,
			Message: message, SourceEventType: event.Event.Type})
	}
	return entries
}
