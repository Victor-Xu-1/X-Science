package server

import (
	"reflect"
	"testing"
)

func TestRecoveredAutonomousPlanCorrectionUsesCurrentPolicy(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	_ = revisePlanForTest(t, f, "recovered-navigation-plan", revisionPlanInput("Explain supplied result"))
	remaining, err := f.server.incompleteGeneratedPlanCondition(f.stream.SessionID)
	if err != nil || remaining == nil {
		t.Fatalf("load real durable plan: %#v %v", remaining, err)
	}
	cause := remaining.runnerCorrection()
	correction := recoveredRunnerCorrection{ReasonCode: cause.ReasonCode, Detail: cause.Detail, Condition: cause.Condition}
	advisory, err := f.server.recoveredCompletionCorrectionIsAdvisory(f.stream.SessionID, nil, correction)
	if err != nil || !advisory {
		t.Fatalf("old autonomous bookkeeping correction remains a forced tool obligation: advisory=%t error=%v", advisory, err)
	}
	for _, variant := range []string{"legacy detail", "foreign artifact", "foreign version", "invalid condition", "different reason"} {
		other := correction
		condition := *correction.Condition
		plan := *condition.Plan
		condition.Plan = &plan
		other.Condition = &condition
		switch variant {
		case "legacy detail":
			other.Condition = nil
		case "foreign artifact":
			plan.ArtifactID = "foreign-plan"
		case "foreign version":
			plan.VersionID = "foreign-version"
		case "invalid condition":
			condition.Schema = "untrusted-condition"
		case "different reason":
			condition.ReasonCode = "plan_approval_required"
		}
		if advisory, err := f.server.recoveredCompletionCorrectionIsAdvisory(f.stream.SessionID, nil, other); err != nil || advisory {
			t.Fatalf("%s condition erased an obligation: advisory=%t error=%v", variant, advisory, err)
		}
	}
	if advisory, err := f.server.recoveredCompletionCorrectionIsAdvisory("different-frame", nil, correction); err != nil || advisory {
		t.Fatalf("another frame cleared the original condition: advisory=%t error=%v", advisory, err)
	}
	for _, otherRun := range []*sessionRunnerChatRun{
		{TaskIntentID: "another-input"},
		{TaskIntentRevision: 999},
		{TaskIntent: "a different request"},
	} {
		if advisory, err := f.server.recoveredCompletionCorrectionIsAdvisory(f.stream.SessionID, otherRun, correction); err != nil || advisory {
			t.Fatalf("another task input cleared the original condition: advisory=%t error=%v", advisory, err)
		}
	}
	metadata, found, err := f.store.GetFrameRuntimeMetadata(f.stream.SessionID)
	if err != nil || !found {
		t.Fatal(err)
	}
	metadata.ContextData["_plan_approved"] = true
	if _, err := f.store.SetFrameRuntimeMetadata(f.stream.SessionID, metadata); err != nil {
		t.Fatal(err)
	}
	advisory, err = f.server.recoveredCompletionCorrectionIsAdvisory(f.stream.SessionID, nil, correction)
	if err != nil || advisory {
		t.Fatalf("explicitly approved plan lost its obligation: advisory=%t error=%v", advisory, err)
	}
	remaining, err = f.server.incompleteGeneratedPlanCondition(f.stream.SessionID)
	if err != nil || remaining == nil || len(remaining.condition.Steps) != 1 {
		t.Fatalf("recovered policy rewrote pending progress: %#v %v", remaining, err)
	}
}

func TestCompactPlanExecutionRepairRetainsVerifiedReceiptAndReason(t *testing.T) {
	for _, applied := range []bool{false, true} {
		binding := map[string]any{"call_id": "verified-call", "tool": "python", "event_id": float64(12)}
		continuation := map[string]any{
			"reason": "execution_receipt_required", "detail": "Reuse a verified receipt; do not repeat execution.",
			"eligible_execution_receipts": []any{binding},
		}
		response := map[string]any{
			"ok": true, "step": "analysis", "status": "in_progress", "applied": applied,
			"execution_continuation": continuation, "execution_ref": "verified-call", "execution_binding": binding,
		}
		result := mapValue(compactAgentRuntimeToolResponse(updateStepStatusToolName, response))
		for _, field := range []string{"execution_continuation", "execution_ref", "execution_binding"} {
			if !reflect.DeepEqual(result[field], response[field]) {
				t.Errorf("applied=%t compaction discarded %s: %#v", applied, field, result)
			}
		}
	}
}

func TestCompletionPlanPolicyRetainsReviewedPlanBoundary(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	_ = revisePlanForTest(t, f, "navigation-plan", revisionPlanInput("Inspect evidence"))
	if err := f.server.validateGeneratedPlanCompletion(f.stream.SessionID, nil); err != nil {
		t.Fatalf("autonomous navigation blocked final delivery: %v", err)
	}
	metadata, found, err := f.store.GetFrameRuntimeMetadata(f.stream.SessionID)
	if err != nil || !found {
		t.Fatal(err)
	}
	metadata.ContextData["_plan_approved"] = true
	if _, err := f.store.SetFrameRuntimeMetadata(f.stream.SessionID, metadata); err != nil {
		t.Fatal(err)
	}
	if err := f.server.validateGeneratedPlanCompletion(f.stream.SessionID, nil); err == nil {
		t.Fatal("explicitly reviewed execution obligations became advisory")
	}
	// The policy is read-only and cannot mark the work completed to obtain a pass.
	remaining, err := f.server.incompleteGeneratedPlanCondition(f.stream.SessionID)
	if err != nil || remaining == nil || len(remaining.condition.Steps) != 1 {
		t.Fatalf("completion policy overwrote plan progress: %#v %v", remaining, err)
	}
}

func TestRecoveredToolContractRecognizesOnlyKernelProtocol(t *testing.T) {
	server := &Server{}
	for _, test := range []struct {
		name       string
		checkpoint sessionRunnerDurableToolCheckpoint
		want       bool
	}{
		{"completed", sessionRunnerDurableToolCheckpoint{ToolName: "bash", ToolCallID: "original-call", ToolPhase: "completed", LifecyclePhase: "recovery"}, true},
		{"failed report", sessionRunnerDurableToolCheckpoint{ToolName: "python", ToolCallID: "original-call", ToolPhase: "failed", LifecyclePhase: "recovery"}, true},
		{"non-kernel", sessionRunnerDurableToolCheckpoint{ToolName: "web_fetch", ToolCallID: "original-call", ToolPhase: "completed", LifecyclePhase: "recovery"}, false},
		{"nonterminal", sessionRunnerDurableToolCheckpoint{ToolName: "bash", ToolCallID: "original-call", ToolPhase: "start", LifecyclePhase: "recovery"}, false},
		{"missing identity", sessionRunnerDurableToolCheckpoint{ToolName: "bash", ToolPhase: "completed", LifecyclePhase: "recovery"}, false},
		{"preflight", sessionRunnerDurableToolCheckpoint{ToolName: "bash", ToolCallID: "original-call", ToolPhase: "completed", LifecyclePhase: "recovery", RejectedBeforeExecution: true}, false},
		{"review", sessionRunnerDurableToolCheckpoint{ToolName: "bash", ToolCallID: "original-call", ToolPhase: "completed", LifecyclePhase: "recovery", ReviewKind: "review"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := server.sessionRunnerDurableCheckpointExplicitTool(test.checkpoint); got != test.want {
				t.Fatalf("recovered receipt accepted=%t, want %t", got, test.want)
			}
		})
	}
}
