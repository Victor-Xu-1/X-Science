package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestConversationSkillSaveFeedbackProseBudgetIncludesLineSeparators(t *testing.T) {
	records := []conversationSkillEvidenceRecord{
		{text: "G", request: true}, {text: "a"}, {text: "b"}, {text: "c"}, {text: "d"}, {text: "R", request: true},
	}
	bytes := 0
	for _, record := range records {
		bytes += len(record.text) + 1
	}
	retained, countedBytes, omitted := trimConversationSkillEvidence(records, bytes, 8)
	actualBytes := 0
	for _, record := range retained {
		actualBytes += len(record.text) + 1
	}
	if len(retained) != 4 || omitted != 2 || countedBytes != actualBytes || actualBytes > 8 ||
		retained[0].text != "G" || retained[len(retained)-1].text != "R" {
		t.Fatalf("prose trimming did not account for serialized line separators: retained=%+v counted=%d actual=%d omitted=%d", retained, countedBytes, actualBytes, omitted)
	}
}

func TestConversationSkillSaveFeedbackPreservesLateFailureDiagnostic(t *testing.T) {
	f := newKernelSkillDraftFixture(t)
	run, _ := loadedClosureFixtureGateway(f)
	result, _ := json.Marshal(map[string]any{"data": strings.Repeat("large observed payload ", 1000), "error": "ORIGINAL_LATE_FAILURE_DIAGNOSTIC"})
	appendConversationFeedbackTerminal(t, f, run, "feedback-late-error", "completed", string(result))
	requireConversationFeedbackCanonicalOutcome(t, f, "feedback-late-error")
	finishSkillControlInput(t, f.agentSaveArtifactsFixture)
	appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-late-error-save", "Hidden Save", true)
	save := claimSkillControlInput(t, f.agentSaveArtifactsFixture)
	source := readConversationFeedbackSource(t, f.agentSaveArtifactsFixture, save)
	feedback := decodeConversationFeedbackSource(t, source.Transcript)
	terminal := findConversationFeedbackTerminal(t, feedback.ToolOutcomes, "feedback-late-error")
	if terminal.ToolPhase != "completed" || !strings.Contains(string(terminal.ToolResult), "ORIGINAL_LATE_FAILURE_DIAGNOSTIC") {
		t.Fatalf("a completed envelope plus large data erased the original failure: %+v", terminal)
	}
	if len(source.Feedback.ToolOutcomes) != 1 || source.Feedback.ToolOutcomes[0].ResultOutcome != agentruntime.ToolResultFailed {
		t.Fatalf("the existing outcome classifier was replaced by lifecycle status: %+v", source.Feedback.ToolOutcomes)
	}
}

func TestConversationSkillSaveFeedbackRetainsClassifierDiagnostics(t *testing.T) {
	failure := map[string]any{"code": "original_failure", "message": "original diagnostic", "recoverable": true, "kind": "source_unavailable"}
	value := map[string]any{
		"data": strings.Repeat("unrelated body ", 1000), "failure": failure,
		"errors": []any{map[string]any{"message": "partial observation"}}, "sourceUnavailable": true,
		"sources": []any{map[string]any{"status": "unavailable"}}, "stopReason": "incomplete",
		"executed": false, "preflight": true,
		"result": map[string]any{"failure": failure, "stop_reason": "failed"},
	}
	diagnostics, err := conversationSkillFeedbackDiagnostics(value, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"failure", "errors", "sourceUnavailable", "sources", "stopReason", "executed", "preflight"} {
		want, _ := json.Marshal(value[field])
		got, _ := json.Marshal(diagnostics[field])
		if string(got) != string(want) {
			t.Fatalf("original classifier diagnostic %s was lost: got=%s want=%s", field, got, want)
		}
	}
	if _, copied := diagnostics["data"]; copied {
		t.Fatal("diagnostic preservation copied arbitrary result records")
	}
	nested, _ := diagnostics["result"].(map[string]any)
	if nested["failure"] == nil || nested["stop_reason"] == nil {
		t.Fatal("one classifier-owned nested envelope lost its original failure")
	}
	// Each recognized field is independently bounded; arbitrary deeper payloads
	// are not traversed and oversized values remain explicitly truncated data.
	for field := range value {
		if field != "result" {
			value[field] = strings.Repeat("large diagnostic ", 1000)
		}
	}
	diagnostics, err = conversationSkillFeedbackDiagnostics(value, true)
	raw, marshalErr := json.Marshal(diagnostics)
	if err != nil || marshalErr != nil || len(raw) > 24<<10 || !strings.Contains(string(raw), `"truncated":true`) {
		t.Fatalf("diagnostic projection exceeded its bounded field envelope: bytes=%d err=%v marshal=%v", len(raw), err, marshalErr)
	}
}

func TestConversationSkillSaveFeedbackRejectsPayloadIdentityLookalikes(t *testing.T) {
	for _, mutation := range []string{"payload-only", "foreign-owner", "foreign-stream", "historical-input-mismatch"} {
		t.Run(mutation, func(t *testing.T) {
			f := newKernelSkillDraftFixture(t)
			disableLoadedClosureFixtureMCP(t, f)
			const name, callID = "feedback-unknown-contract", "feedback-untrusted-load"
			if _, err := f.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "A bounded original host contract.")); err != nil {
				t.Fatal(err)
			}
			publishLoadedClosureFixture(t, f, name, false)
			run, gateway := loadedClosureFixtureGateway(f)
			options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: f.claim.RunnerID}
			call := agentruntime.ToolCall{ID: callID, Name: "skill", Arguments: json.RawMessage(`{"skill":"feedback-unknown-contract"}`)}
			if err := f.server.checkpointChatModelToolCalls(options, run, []agentruntime.ToolCall{call}); err != nil {
				t.Fatal(err)
			}
			ctx := withTranscriptRunnerChatRun(context.Background(), run)
			if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
				Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: callID, Arguments: string(call.Arguments),
			}); err != nil {
				t.Fatal(err)
			}
			loaded, err := gateway.Execute(ctx, call)
			if err != nil || !loaded.SkillLoadReceipt.BoundToCall(callID) {
				t.Fatalf("real host receipt=%v err=%v", loaded.SkillLoadReceipt != nil, err)
			}
			receipt := loaded.SkillLoadReceipt.Clone()
			switch mutation {
			case "foreign-owner":
				receipt.Binding.OwnerID = "other-owner"
			case "foreign-stream":
				receipt.Binding.StreamUID = "other-stream"
			case "historical-input-mismatch":
				receipt.Binding.InputRevision++
			}
			// Deliberately use the lower checkpoint adapter to preserve a legacy
			// or transplanted shape. The ordinary current producer would strip
			// such a receipt before writing it; read-side shape is not authority.
			details := map[string]any{"toolName": "skill", "toolInput": decodeToolEventJSON(string(call.Arguments)),
				"toolResult": map[string]any{"ok": true, "skill": name, "load_receipt": receipt, "body_hash": receipt.ClosureSHA256}}
			if mutation != "payload-only" {
				details[runtimeSkillLoadReceiptField] = receipt
			}
			if err := f.server.checkpointChatTool(options, run, "completed", "Recorded legacy-shaped load", callID, "completed", details); err != nil {
				t.Fatal(err)
			}
			requireConversationFeedbackCanonicalOutcome(t, f, callID)
			appendConversationFeedbackTerminal(t, f, run, "feedback-after-unknown", "completed", `{"ok":true}`)
			requireConversationFeedbackCanonicalOutcome(t, f, "feedback-after-unknown")
			finishSkillControlInput(t, f.agentSaveArtifactsFixture)
			appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-unknown-save", "Hidden Save", true)
			save := claimSkillControlInput(t, f.agentSaveArtifactsFixture)
			feedback := decodeConversationFeedbackSource(t, readConversationFeedbackSource(t, f.agentSaveArtifactsFixture, save).Transcript)
			if len(feedback.Loads) != 1 || feedback.Loads[0].IdentityStatus != "identity_unknown" || feedback.Loads[0].Receipt != nil {
				t.Fatalf("%s acquired a host identity from canonical ownership plus payload shape: %+v", mutation, feedback.Loads)
			}
			terminal := findConversationFeedbackTerminal(t, feedback.ToolOutcomes, "feedback-after-unknown")
			if len(terminal.PriorLoadInvocationKeys) != 0 {
				t.Fatalf("unknown load became an active attribution: %+v", terminal)
			}
		})
	}
}

func TestConversationSkillSaveFeedbackBoundsMetadataWithoutCrossInputLoadReuse(t *testing.T) {
	f := newKernelSkillDraftFixture(t)
	disableLoadedClosureFixtureMCP(t, f)
	const name = "feedback-same-contract-different-inputs"
	if _, err := f.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Inspect the bounded procedure.")); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, f, name, false)
	args, _ := json.Marshal(map[string]any{"skill": name})
	firstRun, firstGateway := loadedClosureFixtureGateway(f)
	first := appendConversationFeedbackLoad(t, f, firstRun, firstGateway, "feedback-prior-input-load", args, args)
	firstOrigin := requireConversationFeedbackCanonicalOutcome(t, f, "feedback-prior-input-load")
	finishSkillControlInput(t, f.agentSaveArtifactsFixture)
	appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-independent-input", "Start an independent procedure; do not inherit an earlier active load.", false)
	claimSkillControlInput(t, f.agentSaveArtifactsFixture)
	nextRun, nextGateway := loadedClosureFixtureGateway(f)
	var last agentruntime.ToolResult
	var lastCall string
	// Real successful durable repeated loads force both the metadata count and
	// byte bounds. No model, external service or scientific compute is involved.
	for index := 0; index < 140; index++ {
		lastCall = fmt.Sprintf("feedback-current-load-%03d", index)
		last = appendConversationFeedbackLoad(t, f, nextRun, nextGateway, lastCall, args, args)
	}
	lastOrigin := requireConversationFeedbackCanonicalOutcome(t, f, lastCall)
	if first.SkillLoadReceipt.InvocationKey != last.SkillLoadReceipt.InvocationKey ||
		first.SkillLoadReceipt.Binding.InputRevision == last.SkillLoadReceipt.Binding.InputRevision {
		t.Fatal("fixture did not create the same exact contract in distinct admitted inputs")
	}
	appendConversationFeedbackTerminal(t, f, nextRun, "feedback-after-current-load", "completed", `{"ok":false,"error":"COMPLETED_ENVELOPE_CONTAINS_FAILURE"}`)
	requireConversationFeedbackCanonicalOutcome(t, f, "feedback-after-current-load")
	for index := 0; index < 30; index++ {
		payload, _ := json.Marshal(map[string]any{"text": fmt.Sprintf("Long recorded trace %d: %s", index, strings.Repeat("observed evidence ", 1000))})
		if _, _, err := f.repo.AppendRunnerEvent(context.Background(), transcriptstore.AppendEventInput{
			Claim: f.claim, ClientMessageID: fmt.Sprintf("feedback-long-trace-%d", index), Type: "assistant_message",
			Source: transcriptstore.EventSourcePayload, PayloadJSON: payload,
		}); err != nil {
			t.Fatal(err)
		}
	}
	finishSkillControlInput(t, f.agentSaveArtifactsFixture)
	appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-budget-correction", "LATEST_FEEDBACK_CORRECTION must remain visible.", false)
	claimSkillControlInput(t, f.agentSaveArtifactsFixture)
	finishSkillControlInput(t, f.agentSaveArtifactsFixture)
	appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-budget-save", "Hidden Save", true)
	save := claimSkillControlInput(t, f.agentSaveArtifactsFixture)
	source := readConversationFeedbackSource(t, f.agentSaveArtifactsFixture, save)
	feedback := decodeConversationFeedbackSource(t, source.Transcript)
	if feedback.Omitted == 0 || source.Omitted == 0 || len(source.Transcript) > conversationSkillTranscriptMaxBytes {
		t.Fatalf("metadata/prose bounds or omissions were lost: metadata=%d prose=%d bytes=%d", feedback.Omitted, source.Omitted, len(source.Transcript))
	}
	for _, line := range strings.Split(source.Transcript, "\n") {
		if strings.Contains(line, `"skill_execution_feedback"`) && len(line) > 64<<10 {
			t.Fatalf("independent metadata byte bound was exceeded: %d", len(line))
		}
	}
	if len(feedback.Loads)+len(feedback.ToolOutcomes)+len(feedback.UserFeedback) > 128 {
		t.Fatal("independent metadata record bound was exceeded")
	}
	for _, load := range feedback.Loads {
		if load.EventID == firstOrigin.TerminalEventID || load.Receipt == nil || !load.Receipt.Valid() {
			t.Fatalf("old input survived eviction, or a full receipt was silently truncated: %+v", load)
		}
	}
	terminal := findConversationFeedbackTerminal(t, feedback.ToolOutcomes, "feedback-after-current-load")
	if !sameConversationFeedbackKeys(terminal.PriorLoadInvocationKeys, last.SkillLoadReceipt.InvocationKey) ||
		!strings.Contains(string(terminal.ToolResult), "COMPLETED_ENVELOPE_CONTAINS_FAILURE") {
		t.Fatalf("latest observation was starved by repeated loads or an error was erased: %+v", terminal)
	}
	for _, record := range source.Feedback.ToolOutcomes {
		if record.ToolCallID == "feedback-after-current-load" &&
			(len(record.PriorLoadEventIDs) != 1 || record.PriorLoadEventIDs[0] != lastOrigin.TerminalEventID) {
			t.Fatalf("same InvocationKey borrowed a load from another admitted input: %+v", record)
		}
	}
	for _, required := range []string{"Create scientific artifacts.", "LATEST_FEEDBACK_CORRECTION"} {
		if !strings.Contains(source.Transcript, required) {
			t.Fatalf("shared budget displaced the original goal or latest correction: %s", required)
		}
	}
}
