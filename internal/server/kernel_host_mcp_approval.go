package server

import (
	"context"
	"errors"
	"strings"
	"time"

	kernelruntime "synon-go/internal/kernel"
	workspace "synon-go/internal/persistence/workspace"
)

// The runtime approval remains the decision authority. This is its existing
// owner-scoped task-input projection, not a second grant or an executor.
func (s *Server) requireVisibleKernelMCPApproval(
	ctx context.Context, access workspace.KernelFrameAccess,
	resolution kernelMCPResolution, input, permission map[string]any,
) (waitErr error) {
	approvalID := strings.TrimSpace(stringValue(permission["approvalId"]))
	if approvalID == "" {
		return kernelruntime.NewHostCallError("approval_unavailable", "MCP approval request is unavailable")
	}
	entry, found, err := s.runtimeStore.Get(agentRuntimeApprovalNamespace, approvalID)
	if err != nil || !found {
		return kernelruntime.NewHostCallError("approval_unavailable", "MCP approval authority is unavailable")
	}
	value := mapValue(entry.Value)
	if value["approvalSource"] != "kernel-host-mcp" || value["frameId"] != access.Frame.ID ||
		value["sessionId"] != access.Frame.ID || value["tool"] != resolution.tool.Name ||
		strings.TrimSpace(stringValue(value["toolCallId"])) == "" {
		return kernelruntime.NewHostCallError("permission_denied", "MCP approval authority does not match this task")
	}
	defer func() {
		if waitErr != nil {
			if err := s.retirePendingKernelHostApproval(approvalID, "MCP approval could not complete"); err != nil {
				waitErr = errors.Join(waitErr, err)
			}
		}
		if err := s.clearKernelMCPApprovalProjection(access, approvalID); err != nil {
			waitErr = errors.Join(waitErr, err)
		}
	}()
	if err := s.retireExpiredKernelMCPApprovals(ctx, access); err != nil {
		return err
	}
	if value["status"] != "pending" {
		return s.waitForKernelMCPApproval(ctx, approvalID)
	}
	mode := "rw"
	if resolution.tool.ReadOnlyHint {
		mode = "ro"
	}
	preview, err := kernelMCPApprovalPreview(resolution, input)
	if err != nil {
		return kernelruntime.NewHostCallError("approval_unavailable", "MCP approval preview is unavailable")
	}
	request := map[string]any{
		"requestId": approvalID, "kind": agentToolApprovalKind,
		"approval_id": approvalID, "approval_source": "kernel-host-mcp",
		"frame_id": access.Frame.ID, "tool_call_id": value["toolCallId"],
		"tool": resolution.tool.Name, "tool_name": resolution.tool.Name,
		"title":       "Approve MCP operation: " + resolution.tool.ToolName,
		"description": "MCP " + resolution.connector.Name + " / " + resolution.tool.ToolName,
		"target":      resolution.connector.Name + "/" + resolution.tool.ToolName,
		"code":        preview, "mode": mode,
		"rememberable": boolValue(permission["rememberable"], false),
	}
	if err := s.workspaceStore.AddKernelArtifactApprovalRequest(ctx, access.UserID, access.Frame.ProjectID, access.Frame.ID, request); err != nil {
		return kernelruntime.NewHostCallError("approval_unavailable", "MCP approval could not be presented")
	}
	frameContext, found, err := s.workspaceStore.GetFrameRealtimeContext(access.Frame.ID)
	if err != nil || !found {
		return kernelruntime.NewHostCallError("approval_unavailable", "MCP approval frame is unavailable")
	}
	if err := s.publishWebConfirmationProjection(frameContext, workspace.FrameEvent{}); err != nil {
		return kernelruntime.NewHostCallError("approval_unavailable", "MCP approval could not be published")
	}
	return s.waitForKernelMCPApproval(ctx, approvalID)
}

func (s *Server) clearKernelMCPApprovalProjection(access workspace.KernelFrameAccess, approvalID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.workspaceStore.RemoveKernelArtifactApprovalRequest(ctx, access.UserID, access.Frame.ProjectID, access.Frame.ID, approvalID); err != nil {
		return err
	}
	current, found, err := s.workspaceStore.GetCompatibilityFrame(access.Frame.ID)
	if err != nil || !found {
		return err
	}
	return s.publishWebConfirmationRemovals(access.Frame.ID, []string{approvalID}, current.Status)
}

// A cancelled host audit is durable proof that its unresolved request cannot
// wake a live call. Retire only this owner's task references, never a decision.
func (s *Server) retireExpiredKernelMCPApprovals(ctx context.Context, access workspace.KernelFrameAccess) error {
	entries, err := s.runtimeStore.ListReadOnly(agentRuntimeApprovalNamespace)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		value := mapValue(entry.Value)
		if value["approvalSource"] != "kernel-host-mcp" || value["status"] != "pending" ||
			value["frameId"] != access.Frame.ID || value["sessionId"] != access.Frame.ID {
			continue
		}
		status, found, err := s.workspaceStore.KernelMCPAuditTerminalStatus(ctx, access.UserID, access.Frame.ID, stringValue(value["toolCallId"]))
		if err != nil {
			return err
		}
		if !found || status != "cancelled" && status != "failed" && status != "blocked" {
			continue
		}
		if err := s.retirePendingKernelHostApproval(entry.Key, "MCP host call has already ended"); err != nil {
			return err
		}
		if err := s.clearKernelMCPApprovalProjection(access, entry.Key); err != nil {
			return err
		}
	}
	return nil
}
