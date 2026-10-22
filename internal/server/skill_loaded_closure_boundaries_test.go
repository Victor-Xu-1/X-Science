package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
)

func TestSkillLoadReplayRejectsChangedDependencyTopologyWithoutOrphanInjection(t *testing.T) {
	for _, change := range []string{"replace", "add", "remove"} {
		t.Run(change, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			disableLoadedClosureFixtureMCP(t, fixture)
			const root, oldDependency, newDependency = "topology-root", "topology-original", "topology-replacement"
			for _, dependency := range []string{oldDependency, newDependency} {
				if _, err := fixture.call("host.skills.edit", dependency, "SKILL.md", kernelSkillManifestForTest(dependency, "Independent package "+dependency)); err != nil {
					t.Fatal(err)
				}
				publishLoadedClosureFixture(t, fixture, dependency, false)
			}
			before, after := oldDependency, newDependency
			if change == "add" {
				before = ""
			}
			if change == "remove" {
				after = ""
			}
			manifest := func(dependency string) string {
				if dependency == "" {
					return loadedClosureDependencyManifest(root, "Root instructions.")
				}
				return loadedClosureDependencyManifest(root, "Root instructions.", dependency)
			}
			if _, err := fixture.call("host.skills.edit", root, "SKILL.md", manifest(before)); err != nil {
				t.Fatal(err)
			}
			publishLoadedClosureFixture(t, fixture, root, false)
			run, gateway := loadedClosureFixtureGateway(fixture)
			old := executeLoadedClosureFixture(t, fixture, run, gateway, "load-original-topology", root, "")
			if before == "" {
				assertLoadedClosureOrder(t, old.SkillLoadReceipt, root)
			} else {
				assertLoadedClosureOrder(t, old.SkillLoadReceipt, before, root)
			}
			if _, err := fixture.call("host.skills.edit", root, "SKILL.md", manifest(after), manifest(before)); err != nil {
				t.Fatal(err)
			}
			publishLoadedClosureFixture(t, fixture, root, true)
			// Old executed names include the old dependency as well as the root.
			roots := []string{root}
			if before != "" {
				roots = append(roots, before)
			}
			snapshot := selectedLoadedClosureFixture(t, fixture, roots...)
			filtered, keys, changed := snapshot.forReplay([]*agentruntime.SkillLoadReceipt{old.SkillLoadReceipt}, nil)
			if len(filtered.contracts) != 0 || len(keys) != 0 || strings.Join(changed, ",") != root {
				t.Fatalf("changed graph injected an orphan/partial closure: items=%d keys=%d changed=%v", len(filtered.contracts), len(keys), changed)
			}
			if after != "" {
				filtered, keys, _ = snapshot.forReplay([]*agentruntime.SkillLoadReceipt{old.SkillLoadReceipt}, []string{after})
				if len(filtered.contracts) != 1 || filtered.contracts[0].skill.Name != after || len(keys) != 0 {
					t.Fatal("an independent explicit root was lost or gained historical exact authority")
				}
			}
		})
	}
}

func TestSkillLoadContinuityBoundsReceiptsWithoutLosingSourceFacts(t *testing.T) {
	contracts := make([]agentruntime.SkillContractIdentity, agentruntime.MaxSkillLoadContracts)
	for index := range contracts {
		contracts[index] = agentruntime.SkillContractIdentity{
			Name: fmt.Sprintf("fixture-%s-%02d", strings.Repeat("x", 400), index), Origin: "personal", BundleKind: "files",
			ContractSHA256: strings.Repeat("a", 64), BundleSHA256: strings.Repeat("b", 64),
		}
	}
	var records []sessionRunnerToolContinuityRecord
	for index := 0; index < sessionRunnerContinuityRecentLimit; index++ {
		receipt, err := agentruntime.NewSkillLoadReceipt(contracts[len(contracts)-1].Name, "scope", "", contracts)
		if err != nil {
			t.Fatal(err)
		}
		callID := fmt.Sprintf("bounded-load-%d", index)
		receipt.Binding = &agentruntime.SkillLoadBinding{StreamUID: "bounded-stream", OwnerID: "bounded-owner", InputRevision: 1, RunnerAttempt: 1, ToolCallID: callID}
		entry := eventjournal.Entry{EventID: int64(index + 1), ClientMessageID: callID}
		records = append(records, sessionRunnerToolContinuityRecord{
			EventID: entry.EventID, ToolCallID: callID, ToolName: "skill", Phase: "completed", Successful: true,
			SkillName: receipt.SkillName, InputSummary: "Original summary.",
			SkillLoadReceipt: receipt, SkillLoadSource: runtimeSkillLoadSourceForEntry(entry, receipt),
		})
	}
	bounded := boundSessionRunnerToolContinuity(records)
	projectedBytes, referenced := 0, 0
	for _, record := range bounded {
		encoded, err := json.Marshal(struct {
			Receipt *agentruntime.SkillLoadReceipt `json:"skillLoadReceipt,omitempty"`
			Source  *runtimeSkillLoadSource        `json:"skillLoadSource,omitempty"`
		}{record.SkillLoadReceipt, record.SkillLoadSource})
		if err != nil {
			t.Fatal(err)
		}
		projectedBytes += len(encoded)
		if !record.SkillLoadSource.valid() || record.InputSummary != "Original summary." || record.SkillName == "" {
			t.Fatal("byte bounding discarded original continuity facts or source location")
		}
		if record.SkillLoadReceipt == nil {
			referenced++
		}
	}
	if len(bounded) != len(records) || referenced == 0 || projectedBytes > runtimeSkillLoadContinuityBytes {
		t.Fatalf("receipt projection is not bounded: records=%d referenced=%d bytes=%d", len(bounded), referenced, projectedBytes)
	}
}

func TestSkillLoadSourceReferenceRestoresOnlyVisibleFrozenReplayCheckpoint(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "reference-replay-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Original referenced instructions.")); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	run, gateway := loadedClosureFixtureGateway(fixture)
	loaded := executeLoadedClosureFixture(t, fixture, run, gateway, "load-source-reference", name, "scope")
	continuity, err := sessionRunnerToolContinuityRecords(readLoadedClosureEntries(t, fixture.repo, fixture.stream))
	if err != nil || len(continuity) != 1 || !continuity[0].SkillLoadSource.valid() {
		t.Fatalf("actual checkpoint did not produce a source locator: records=%d err=%v", len(continuity), err)
	}
	// The independent budget test proves this projection. Keep the real source
	// receipt unchanged; only omit its optional copy from this compact ledger.
	continuity[0].SkillLoadReceipt = nil
	if err := fixture.server.checkpointChatTool(SessionRunnerChatOptions{RunnerID: run.Transcript.Claim.RunnerID}, run,
		"running", "Retain source-indexed load history.", "", "auto_compact", map[string]any{sessionRunnerToolContinuityField: continuity}); err != nil {
		t.Fatal(err)
	}
	entries := readLoadedClosureEntries(t, fixture.repo, fixture.stream)
	var compacted []eventjournal.Entry
	for _, entry := range entries {
		if stringValue(entry.Message["toolPhase"]) == "auto_compact" {
			compacted = []eventjournal.Entry{entry}
		}
	}
	snapshot, err := fixture.repo.GetProjectionSnapshot(context.Background(), fixture.stream.UID, fixture.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range []string{"exact", "no-replay-snapshot", "wrong-generation", "before-source", "wrong-digest", "wrong-event", "cancelled"} {
		t.Run(boundary, func(t *testing.T) {
			selected := snapshot
			run.Transcript.replaySnapshot = &selected
			raw, _ := json.Marshal(compacted)
			var candidate []eventjournal.Entry
			if err := json.Unmarshal(raw, &candidate); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch boundary {
			case "no-replay-snapshot":
				run.Transcript.replaySnapshot = nil
			case "wrong-generation":
				selected.BranchGeneration++
			case "before-source":
				selected.ThroughPublicationSequence = 1
			case "wrong-digest", "wrong-event":
				records, _, _ := latestPersistedSessionRunnerToolContinuity(candidate)
				if boundary == "wrong-digest" {
					records[0].SkillLoadSource.ReceiptSHA256 = strings.Repeat("f", 64)
				} else {
					records[0].EventID++
					records[0].SkillLoadSource.EventID++
				}
				candidate[0].Message[sessionRunnerToolContinuityField] = records
			case "cancelled":
				cancel()
			}
			receipts, err := fixture.server.restoreSkillLoadReceipts(ctx, candidate, run)
			if boundary == "cancelled" {
				if err == nil || len(receipts) != 0 {
					t.Fatal("cancelled source read became successful restore")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "exact" {
				if len(receipts) != 1 || receipts[0].InvocationKey != loaded.SkillLoadReceipt.InvocationKey {
					t.Fatal("visible original source receipt did not restore")
				}
			} else if len(receipts) != 0 {
				t.Fatal("unverified source locator acquired exact load authority")
			}
		})
	}
}
