package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/toolcontract"
)

func TestRunnerSourceObservationCanonicalSQLitePagingRestart(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	var projectionRun *sessionRunnerChatRun
	for i, label := range []string{"pmids:", "pmids:", "pmIds:", "pmids:"} {
		callID := fmt.Sprint("source-observation-", i)
		authority, operationRun := largeMCPStartedOperation(t, fixture, callID)
		arguments := largeMCPREPLArguments
		if err := fixture.server.checkpointSessionRunnerToolEvent(ctx, SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, operationRun, agentruntime.Event{Type: agentruntime.EventToolStarted, ToolName: "repl", ToolCallID: callID, Arguments: arguments}); err != nil {
			t.Fatal(err)
		}
		if projectionRun == nil {
			projectionRun = operationRun
		}
		if i == 3 {
			// A new process has no cached observations. It must derive the same
			// window from this owner/input/active branch, not recent model text.
			projectionRun = &sessionRunnerChatRun{SessionID: operationRun.SessionID, Transcript: operationRun.Transcript}
		}
		input, output := map[string]any{"query": "controlled source", "page": 1}, map[string]any{"total_count": 0, "returned_count": 0, "has_more": false}
		call := kernelruntime.HostCall{ID: fmt.Sprintf("hc-%032x", i+1), CellID: authority.operation.ExecutionID}
		audit := workspace.KernelMCPAuditInput{CallID: call.ID, FrameID: fixture.stream.FrameID, RootFrameID: fixture.stream.RootFrameID,
			OwnerUserID: fixture.stream.OwnerID, Server: "fixture", Method: "search", Input: input}
		if _, err := fixture.server.workspaceStore.BeginKernelMCPAudit(ctx, audit); err != nil {
			t.Fatal(err)
		}
		attestation := kernelMCPSourceEvidenceAttestation{Class: workspace.KernelMCPEvidenceClassBundledReadOnly, ConnectorID: "bundled:fixture", ConnectorSource: "bundled", InputSchemaSHA256: strings.Repeat("a", 64), ReadOnlyHint: true}
		if err := fixture.server.commitKernelMCPSourceEvidence(ctx, authority, audit, call, "mcp__fixture__search", input, output, attestation); err != nil {
			t.Fatal(err)
		}
		value := map[string]any{"ok": true, "exec_id": authority.operation.ExecutionID, "stdout": label, "files_written": []any{}, "dropped_roots": []any{}}
		raw, _ := json.Marshal(value)
		op := authority.operation
		if _, err := fixture.server.workspaceStore.FinishKernelLocalOperation(ctx, workspace.FinishKernelLocalOperationInput{
			OwnerUserID: op.OwnerUserID, OperationID: op.OperationID, ExpectedStateVersion: op.StateVersion,
			Claim: fixture.claim, BootID: "large-source-boot", ExecutionID: op.ExecutionID, TerminalState: workspace.KernelLocalOperationStateCompleted, ReasonCode: "execution_completed", TerminalResultJSON: raw,
			ExecutionLog: workspace.SaveExecutionLogInput{Record: workspace.ExecutionLogRecord{ID: op.ExecutionID, FrameID: op.FrameID, CellIndex: i, KernelID: op.KernelID, CondaEnv: op.Environment, Language: "python", Source: "import host", Stdout: label, ExitStatus: "completed", Origin: "agent", ExecutedAt: time.Now().UTC()},
				ExpectedOwnerID: op.OwnerUserID, ExpectedProjectID: op.ProjectID, ExpectedFrameIncarnationID: op.FrameIncarnationID, ExpectedRootFrameIncarnationID: op.RootFrameIncarnationID},
		}); err != nil {
			t.Fatal(err)
		}
		observed := fixture.server.runnerToolSourceObservation(ctx, projectionRun, agentruntime.ToolCall{ID: callID, Name: "repl"}, value)
		if observed == nil || observed.RepeatedOperations != i {
			t.Fatalf("step %d canonical observation: %+v", i, observed)
		}
		if err := fixture.server.checkpointSessionRunnerToolEvent(ctx, SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, operationRun, agentruntime.Event{Type: agentruntime.EventToolCompleted, ToolName: "repl", ToolCallID: callID, Arguments: arguments, Result: string(raw)}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			for n := 0; n < 130; n++ {
				if _, err := fixture.server.checkpointTranscriptRunnerEvent(ctx, operationRun.Transcript, transcriptstore.RunnerPhaseExecuting, fmt.Sprint("source-observation-padding-", n), map[string]any{"sourceObservationTestPadding": n}, false); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if observation := fixture.server.runnerToolSourceObservation(ctx, projectionRun, agentruntime.ToolCall{ID: "unknown-actual-cell", Name: "repl"}, map[string]any{"ok": true, "stdout": "same label", "files_written": []any{}}); observation.State != agentruntime.SourceObservationUnknown {
		t.Fatalf("unattested cell invented verified source content: %+v", observation)
	}
	// Reproduce the mutable-attempt shape of a legitimate reclaimed resume in
	// this temporary SQLite fixture. Projection must use canonical input events,
	// never this updated attempt row to relabel the older source checkpoints.
	if _, _, err := fixture.repo.AppendUserEvent(ctx, transcriptstore.AppendUserEventInput{StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, ClientMessageID: "source-new-input", PayloadJSON: []byte(`{"role":"user","text":"A different research goal"}`)}); err != nil {
		t.Fatal(err)
	}
	fixture.claim.ClaimedInputRevision++
	if _, err := fixture.db.Exec(`UPDATE transcript_runner_attempts SET claimed_input_revision=? WHERE stream_uid=? AND attempt=?`, fixture.claim.ClaimedInputRevision, fixture.stream.UID, fixture.claim.Attempt); err != nil {
		t.Fatal(err)
	}
	projectionRun = &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	if observation := fixture.server.runnerToolSourceObservation(ctx, projectionRun, agentruntime.ToolCall{ID: "new-input-cell", Name: "repl"}, map[string]any{"ok": true}); observation.State != agentruntime.SourceObservationUnknown || observation.RepeatedOperations != 0 {
		t.Fatalf("same-attempt new input inherited source window: %+v", observation)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if observation := fixture.server.runnerToolSourceObservation(cancelled, projectionRun, agentruntime.ToolCall{ID: "cancelled", Name: "repl"}, map[string]any{"ok": true}); observation.State != agentruntime.SourceObservationUnknown {
		t.Fatalf("cancelled projection invented source evidence: %+v", observation)
	}
}

func TestRunnerSourceObservationBothReceiptFormatsRequireOriginalBlob(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	authority, run := largeMCPStartedOperation(t, fixture)
	call := kernelruntime.HostCall{ID: "hc-0123456789abcdef0123456789abcdef", CellID: authority.operation.ExecutionID}
	input := map[string]any{"query": "controlled source"}
	output := map[string]any{"records": []any{}, "padding": strings.Repeat("preserved-source ", 2000)}
	audit := workspace.KernelMCPAuditInput{CallID: call.ID, FrameID: fixture.stream.FrameID, RootFrameID: fixture.stream.RootFrameID, OwnerUserID: fixture.stream.OwnerID, Server: "fixture", Method: "search", Input: input}
	if _, err := fixture.store.BeginKernelMCPAudit(ctx, audit); err != nil {
		t.Fatal(err)
	}
	attestation := kernelMCPSourceEvidenceAttestation{Class: workspace.KernelMCPEvidenceClassBundledReadOnly, ConnectorID: "bundled:fixture", ConnectorSource: "bundled", InputSchemaSHA256: strings.Repeat("a", 64), ReadOnlyHint: true}
	if err := fixture.server.commitKernelMCPSourceEvidence(ctx, authority, audit, call, "mcp__fixture__search", input, output, attestation); err != nil {
		t.Fatal(err)
	}
	events, err := fixture.repo.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var entry eventjournal.Entry
	for _, projected := range events {
		var message eventjournal.Message
		if json.Unmarshal(projected.ResolvedPayloadJSON, &message) == nil && message["toolCallId"] == call.ID {
			message["type"] = "runner_checkpoint"
			entry = eventjournal.Entry{EventID: projected.Event.EventID, Message: message, SourceEventType: "runner_checkpoint"}
		}
	}
	validated, err := fixture.server.validateRunnerSourceObservationEntry(ctx, run.Transcript, entry)
	if err != nil {
		t.Fatal(err)
	}
	witness, valid := validated.RuntimeProjection.(runnerSourceObservationWitness)
	if !valid || witness.key == "" {
		t.Fatal("real nested source blob failed normal owner/call/hash restoration")
	}
	direct := copyMapAny(entry.Message)
	for _, key := range []string{"outerToolCallId", "kernelOperationId", "executionId", "hostCallId", "kernelId", "kernelGeneration"} {
		delete(direct, key)
	}
	direct["schema"] = workspaceMCPSourceEvidenceSchemaV1
	encoded, _ := json.Marshal(direct["toolResult"])
	direct["resultSha256"] = kernelMCPEvidenceSHA256(encoded) // direct receipt attests the descriptor envelope
	directEntry := eventjournal.Entry{EventID: entry.EventID, Message: direct, SourceEventType: "runner_checkpoint"}
	validated, err = fixture.server.validateRunnerSourceObservationEntry(ctx, run.Transcript, directEntry)
	if err != nil || validated.RuntimeProjection.(runnerSourceObservationWitness).key != witness.key {
		t.Fatalf("receipt format changed original content identity: %+v %v", validated.RuntimeProjection, err)
	}
	for name, mutate := range map[string]func(eventjournal.Message){
		"bad request hash": func(message eventjournal.Message) { message["requestSha256"] = strings.Repeat("f", 64) },
		"bad original hash": func(message eventjournal.Message) {
			raw, _ := json.Marshal(message["toolResult"])
			descriptor, _, externalized, err := toolcontract.DecodeExternalizedResult(raw)
			if !externalized || err != nil {
				t.Fatal("fixture did not externalize")
			}
			descriptor.SHA256 = strings.Repeat("f", 64)
			message["toolResult"] = descriptor
			encoded, _ := json.Marshal(descriptor)
			message["resultSha256"] = kernelMCPEvidenceSHA256(encoded)
		},
		"missing original": func(message eventjournal.Message) {
			raw, _ := json.Marshal(message["toolResult"])
			descriptor, _, externalized, err := toolcontract.DecodeExternalizedResult(raw)
			if !externalized || err != nil {
				t.Fatal("fixture did not externalize")
			}
			descriptor.ArtifactID = "large-tool-result-" + strings.Repeat("f", 32)
			message["toolResult"] = descriptor
			encoded, _ := json.Marshal(descriptor)
			message["resultSha256"] = kernelMCPEvidenceSHA256(encoded)
		},
	} {
		t.Run(name, func(t *testing.T) {
			message := copyMapAny(direct)
			mutate(message)
			got, err := fixture.server.validateRunnerSourceObservationEntry(ctx, run.Transcript, eventjournal.Entry{EventID: entry.EventID, Message: message, SourceEventType: "runner_checkpoint"})
			if err != nil || got.RuntimeProjection.(runnerSourceObservationWitness).key != "" {
				t.Fatalf("unverified original became source evidence: %+v %v", got.RuntimeProjection, err)
			}
		})
	}
}
