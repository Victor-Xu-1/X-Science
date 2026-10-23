package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestSkillOriginalExternalizedContextRequiresExactPayload(t *testing.T) {
	for _, mutation := range []string{"unchanged", "missing_blob", "same_size_corrupt_blob"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			disableLoadedClosureFixtureMCP(t, fixture)
			const name, args, callID = "externalized-context-fixture", " raw scope alpha ", "load-original-externalized-context"
			const scope = "Before< raw scope alpha >After"
			body := strings.Repeat("synthetic-externalized-body ", 1000) + "Before<$ARGUMENTS>After"
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, body)); err != nil {
				t.Fatal(err)
			}
			publishLoadedClosureFixture(t, fixture, name, false)
			run, gateway := loadedClosureFixtureGateway(fixture)
			ctx := withTranscriptRunnerChatRun(context.Background(), run)
			input, err := json.Marshal(map[string]any{"skill": name, "args": args})
			if err != nil {
				t.Fatal(err)
			}
			call := agentruntime.ToolCall{ID: callID, Name: "skill", Arguments: input}
			options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: run.Transcript.Claim.RunnerID, OutputLimitBytes: 50_000}
			if err := fixture.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
				Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: call.ID, Arguments: string(input),
			}); err != nil {
				t.Fatal(err)
			}
			result, err := gateway.Execute(ctx, call)
			if err != nil || !result.SkillLoadReceipt.BoundToCall(callID) {
				t.Fatalf("actual Skill load did not produce its bound receipt: err=%v", err)
			}
			full, ok := result.Value.(string)
			var executed map[string]any
			if !ok || strings.Count(full, scope) != 1 || strings.Contains(full, "Before<>After") ||
				json.Unmarshal(result.ExecutedArguments, &executed) != nil || executed["args"] != args {
				t.Fatal("actual gateway did not preserve the full rendered body and raw invocation arguments")
			}
			raw, err := json.Marshal(result.Value)
			if err != nil || len(raw) <= 16_000 || len(raw) >= 50_000 {
				t.Fatalf("actual Skill payload must cross the real inline boundary, not gateway capture: bytes=%d err=%v", len(raw), err)
			}
			engine := fixture.server.newAgentRuntimeEngineWithContext(ctx, options)
			materialized, err := engine.MaterializeToolResult(ctx, call, result.Value, agentruntime.ToolResultSucceeded)
			if err != nil {
				t.Fatal(err)
			}
			var descriptor agentruntime.LargeToolResultDescriptor
			if err := json.Unmarshal(materialized.JSON, &descriptor); err != nil ||
				descriptor.ArtifactID == "" || !strings.HasPrefix(descriptor.VersionID, "ltr-") ||
				materialized.ResultRef != "artifact-version:"+descriptor.VersionID || !descriptor.Truncated {
				t.Fatalf("real engine failed to externalize the original Skill result: err=%v", err)
			}
			record, found, err := fixture.store.GetRunnerLargeToolResult(ctx, descriptor.ArtifactID, fixture.stream.OwnerID)
			digest := sha256.Sum256(raw)
			if err != nil || !found || record.OwnerUserID != fixture.stream.OwnerID || record.StreamUID != fixture.stream.UID ||
				record.ProjectID != fixture.stream.ProjectID || record.RootFrameID != fixture.stream.RootFrameID ||
				record.FrameID != fixture.stream.FrameID || record.SourceEventID <= 0 ||
				record.SourceEventID != run.ToolSourceEventIDs[callID] || record.Attempt != fixture.claim.Attempt ||
				record.Attempt != result.SkillLoadReceipt.Binding.RunnerAttempt || record.RunnerID != fixture.claim.RunnerID ||
				record.ToolCallID != callID || record.ToolName != call.Name || record.VersionID != descriptor.VersionID ||
				record.SizeBytes != int64(len(raw)) || record.ContentSHA256 != hex.EncodeToString(digest[:]) ||
				record.ContentSHA256 != descriptor.SHA256 || record.StoragePath == "" {
				t.Fatalf("actual immutable large-result authority did not match its source/call/owner/hash: found=%v err=%v", found, err)
			}
			result.Value = decodeToolEventJSON(string(materialized.JSON))
			if err := completeLoadedClosureFixture(ctx, fixture, options, run, callID, input, result); err != nil {
				t.Fatal(err)
			}
			receiptJSON, err := json.Marshal(result.SkillLoadReceipt)
			if err != nil {
				t.Fatal(err)
			}
			entries, compactID := persistLoadedClosureCompaction(t, fixture, run)
			entries, resumed, _ := reopenLoadedClosureFixture(t, fixture, entries, compactID)
			ctx = withTranscriptRunnerChatRun(context.Background(), resumed)
			if _, err := fixture.server.loadTranscriptRunnerReplay(ctx, resumed.Transcript, 20, 100); err != nil {
				t.Fatal(err)
			}
			current := selectedLoadedClosureFixture(t, fixture, name)

			// Mutate only the one proven synthetic payload, after reopen and the
			// real replay snapshot pin. Never rewrite its metadata or receipt.
			blobRoot, err := filepath.Abs(fixture.databasePath + ".blobs")
			if err != nil {
				t.Fatal(err)
			}
			blobPath := filepath.Join(blobRoot, record.StoragePath)
			resolvedPath, err := filepath.EvalSymlinks(blobPath)
			if err != nil {
				t.Fatal(err)
			}
			relative, err := filepath.Rel(blobRoot, resolvedPath)
			if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
				strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				t.Fatal("refusing to mutate a path outside this isolated fixture's blob root")
			}
			stored, err := os.ReadFile(resolvedPath)
			if err != nil || !bytes.Equal(stored, raw) {
				t.Fatalf("the actual stored blob must equal the complete original JSON: err=%v", err)
			}
			switch mutation {
			case "missing_blob":
				if err := os.Remove(resolvedPath); err != nil {
					t.Fatal(err)
				}
			case "same_size_corrupt_blob":
				corrupt := append([]byte(nil), stored...)
				index := bytes.Index(corrupt, []byte("synthetic-externalized-body"))
				if index < 0 {
					t.Fatal("synthetic body marker not present in the original JSON")
				}
				corrupt[index] = 'S'
				if len(corrupt) != len(stored) || bytes.Equal(corrupt, stored) || !json.Valid(corrupt) {
					t.Fatal("corruption must change content but preserve exact size and valid JSON")
				}
				if err := os.WriteFile(resolvedPath, corrupt, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			receipts, err := fixture.server.restoreSkillLoadReceipts(ctx, entries, resumed)
			if err != nil || len(receipts) != 1 {
				t.Fatalf("compaction lost the genuine original receipt: count=%d err=%v", len(receipts), err)
			}
			restoredJSON, err := json.Marshal(receipts[0])
			if err != nil || !bytes.Equal(restoredJSON, receiptJSON) {
				t.Fatal("payload availability changed the original durable receipt")
			}
			_, prepared, keys, changed, err := fixture.server.prepareRuntimeSkillReplayContext(
				ctx, current, receipts, nil, resumed, resumed.SessionID,
			)
			if err != nil {
				t.Fatalf("unreadable historical payload should require an ordinary reload, not abort preparation: %v", err)
			}
			prompt, err := fixture.server.runtimeSkillContextFromSkills(prepared)
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "unchanged" {
				if len(keys) != 1 || keys[0] != result.SkillLoadReceipt.InvocationKey || len(changed) != 0 ||
					strings.Count(prompt, scope) != 1 || !strings.Contains(prompt, strings.ReplaceAll(body, "$ARGUMENTS", args)) ||
					strings.Contains(prompt, "Before<>After") || strings.Contains(prompt, "$ARGUMENTS") {
					t.Fatalf("exact original payload did not restore the complete scoped body exactly once: keys=%d changed=%v", len(keys), changed)
				}
			} else if len(keys) != 0 || len(changed) != 1 || changed[0] != name ||
				strings.Contains(prompt, "synthetic-externalized-body") || strings.Contains(prompt, scope) ||
				strings.Contains(prompt, "Before<>After") {
				t.Fatalf("missing/corrupt payload reused a preview or unparameterized body instead of requiring reload: keys=%d changed=%v", len(keys), changed)
			}
			if resumed.hasExecutedSkillInvocationKey(result.SkillLoadReceipt.InvocationKey) {
				t.Fatal("preparation alone activated a key without actual provider context inclusion")
			}
			after, found, err := fixture.store.GetRunnerLargeToolResult(ctx, record.ArtifactID, fixture.stream.OwnerID)
			if err != nil || !found || !reflect.DeepEqual(after, record) {
				t.Fatalf("payload restoration changed immutable source/attempt/hash metadata: found=%v err=%v", found, err)
			}
			afterReceipt, err := json.Marshal(result.SkillLoadReceipt)
			if err != nil || !bytes.Equal(afterReceipt, receiptJSON) {
				t.Fatal("context preparation mutated the original live receipt")
			}
		})
	}
}
