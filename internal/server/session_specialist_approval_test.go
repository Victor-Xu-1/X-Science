package server

import (
	"context"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

func TestSessionSpecialistApprovalWaitOnlyAndNoModelConsent(t *testing.T) {
	if !isKernelHostWaitOnlyApprovalSource(kernelSpecialistApprovalSource) {
		t.Fatal("specialist approval could dispatch the whole control tool instead of only releasing its original invocation")
	}
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "specialist-approval-project", "specialist-approval-owner")
	frame, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "specialist-approval-frame", ProjectID: "specialist-approval-project",
		AgentName: "OPERON", Status: "processing", ConversationType: "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	defaults := app.agentRuntimeApprovalDefaults()
	defaults.Mode, defaults.RequireReason, defaults.RememberDecisions = "ask", false, false
	permission := app.queueAgentRuntimeApprovalForSourceWithMetadata("host.agents.switch",
		agentruntime.ToolCall{ID: "synthetic-specialist-invocation", Name: "host.agents.switch"},
		map[string]any{"command_id": "synthetic-command", "desired_agent": "SYNTHETIC_A"},
		"ask", defaults, kernelSpecialistApprovalSource,
		map[string]any{"frameId": frame.ID, "projectId": frame.ProjectID, "sessionId": frame.ID})
	approvalID := stringValue(permission["approvalId"])
	if approvalID == "" {
		t.Fatal("fixture approval was not persisted")
	}
	if value, err := app.resolveAgentRuntimeApprovalMessage(context.Background(), "", map[string]any{
		"approvalId": approvalID, "approve": true,
	}); err == nil || value != nil || !strings.Contains(err.Error(), "authenticated user") {
		t.Fatalf("model-side consent was not rejected at its own authority boundary: value=%#v err=%v", value, err)
	}
	entry, found, err := app.runtimeStore.Get(agentRuntimeApprovalNamespace, approvalID)
	if err != nil || !found || mapValue(entry.Value)["status"] != "pending" {
		t.Fatalf("rejected consent changed the decision: entry=%#v found=%t err=%v", entry, found, err)
	}
	if err := app.retirePendingKernelHostApproval(approvalID, "synthetic original invocation cancelled"); err != nil {
		t.Fatal(err)
	}
	entry, found, err = app.runtimeStore.Get(agentRuntimeApprovalNamespace, approvalID)
	if err != nil || !found || mapValue(entry.Value)["status"] != "failed" || mapValue(entry.Value)["failureCode"] != "approval_wait_ended" {
		t.Fatalf("cancelled invocation left reusable pending authority: entry=%#v found=%t err=%v", entry, found, err)
	}
}
