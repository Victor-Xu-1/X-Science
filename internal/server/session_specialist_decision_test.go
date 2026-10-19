package server

import (
	"testing"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestSessionSpecialistUserDecisionOnlyReleasesOriginalInvocation(t *testing.T) {
	for _, test := range []struct {
		name, action, want            string
		finished, duplicate, rejected bool
		recovered, forgedRevision     bool
	}{
		{name: "approve once", action: "allow", want: "approved"},
		{name: "approve recovered source", action: "allow", recovered: true, want: "approved"},
		{name: "reject forged current revision", action: "allow", forgedRevision: true, want: "pending", rejected: true},
		{name: "deny stale original", action: "deny", finished: true, want: "denied"},
		{name: "reject stale consent", action: "allow", finished: true, want: "pending", rejected: true},
		{name: "reject remembered consent", action: "allow_always", want: "pending", rejected: true},
		{name: "reject duplicate decisions", action: "allow", duplicate: true, want: "pending", rejected: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, store, frame := newSessionSpecialistAPIFixture(t)
			repo := app.transcriptStore
			stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
				UID: "frame:" + frame.ID, OwnerID: specialistFixtureOwner, ExternalID: frame.ID, SessionID: frame.ID,
				Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
				StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "decision-input", FrameEventID: "decision-input-event",
				MessageUUID: "decision-input-message", Text: "Synthetic approval source input.",
			}); err != nil {
				t.Fatal(err)
			}
			claim := specialistClaimFixture(t, repo, stream, "decision-runner")
			evidence, _ := largeMCPStartedOperation(t, &agentSaveArtifactsFixture{
				server: app, store: store, repo: repo, stream: stream, claim: claim,
			}, "specialist-decision-repl")
			op := evidence.operation
			if test.recovered {
				// The original fixture's started operation is unrelated; the
				// recovered operation below has its own accepted source identity.
				op, claim = specialistRecoveredDecisionOperation(t, app, stream, claim)
			}
			definition, err := app.captureSessionSpecialist(t.Context(), stream.OwnerID, "SYNTHETIC_A")
			if err != nil {
				t.Fatal(err)
			}
			metadata := map[string]any{
				"frameId": frame.ID, "projectId": frame.ProjectID, "sessionId": frame.ID,
				"frameIncarnationId": op.FrameIncarnationID, "rootFrameId": op.RootFrameID,
				"rootIncarnationId": op.RootFrameIncarnationID, "operationId": op.OperationID,
				"streamUid": stream.UID, "streamEpoch": stream.Epoch, "runnerId": claim.RunnerID,
				"runnerAttempt": claim.Attempt, "runnerClaimHash": op.RunnerClaimSHA256,
				"claimedInputRevision": claim.ClaimedInputRevision, "kernelId": op.KernelID,
				"kernelGeneration": op.KernelGeneration, "branchId": op.BranchID,
				"branchGeneration": op.BranchGeneration, "snapshotId": definition.ID,
			}
			if test.forgedRevision {
				metadata["claimedInputRevision"] = claim.ClaimedInputRevision + 1
			}
			defaults := app.agentRuntimeApprovalDefaults()
			defaults.Mode, defaults.RequireReason, defaults.RememberDecisions = "ask", false, false
			permission := app.queueAgentRuntimeApprovalForSourceWithMetadata("host.agents.switch",
				agentruntime.ToolCall{ID: "specialist-decision-host-call", Name: "host.agents.switch"},
				map[string]any{"snapshot_id": definition.ID, "desired_agent": definition.SelectionName},
				"ask", defaults, kernelSpecialistApprovalSource, metadata)
			id := stringValue(permission["approvalId"])
			if id == "" {
				t.Fatal("approval was not persisted")
			}
			request := map[string]any{"requestId": id, "kind": agentToolApprovalKind,
				"approval_source": kernelSpecialistApprovalSource, "tool_call_id": "specialist-decision-host-call"}
			if err := store.AddKernelArtifactApprovalRequest(t.Context(), stream.OwnerID, frame.ProjectID, frame.ID, request); err != nil {
				t.Fatal(err)
			}
			if test.finished {
				if _, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{
					Claim: claim, ClientMessageID: "finish-before-specialist-decision", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
				}); err != nil {
					t.Fatal(err)
				}
			}
			compat, found, err := store.GetCompatibilityFrame(frame.ID)
			if err != nil || !found {
				t.Fatalf("compatibility frame found=%t err=%v", found, err)
			}
			responses := []compatibilityInputResponse{{ToolID: id, Action: test.action}}
			if test.duplicate {
				responses = append(responses, responses[0])
			}
			result, handled, err := app.resolveKernelSpecialistApprovalInputs(t.Context(), compat,
				map[string]map[string]any{id: request}, responses)
			if !handled || (err != nil) != test.rejected {
				t.Logf("synthetic original operation: %+v", op)
				t.Logf("synthetic approval metadata: %#v", metadata)
				t.Fatalf("handled=%t err=%v", handled, err)
			}
			entry, found, readErr := app.runtimeStore.Get(agentRuntimeApprovalNamespace, id)
			if readErr != nil || !found || mapValue(entry.Value)["status"] != test.want {
				t.Fatalf("decision=%#v found=%t err=%v", entry, found, readErr)
			}
			value := mapValue(entry.Value)
			if value["executedInput"] != nil || value["rememberedApproval"] != nil {
				t.Fatal("one-shot decision re-executed or remembered the control tool")
			}
			if !test.rejected && len(result.RemainingIDs) != 0 {
				t.Fatalf("resolved request left pending input: %#v", result)
			}
			state := specialistReadbackFixture(t, app, frame.ID)
			if state.Revision != 0 || state.Phase != "legacy" {
				t.Fatal("the decision endpoint itself performed the switch")
			}
		})
	}
}
