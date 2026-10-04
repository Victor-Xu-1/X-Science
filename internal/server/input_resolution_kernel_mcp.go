package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	workspace "synon-go/internal/persistence/workspace"
)

// A host callback is already executing and waiting for this decision. Resolve
// its existing authority without dispatching or replaying the enclosing REPL.
func (s *Server) resolveKernelMCPApprovalInputs(
	ctx context.Context, frame workspace.CompatibilityFrame,
	pendingByID map[string]map[string]any, responses []compatibilityInputResponse,
) (compatibilityResolveInputResult, bool, error) {
	count := 0
	for _, response := range responses {
		id := strings.TrimSpace(firstNonEmpty(response.ToolID, response.RequestID))
		if item := pendingByID[id]; item["kind"] == agentToolApprovalKind && item["approval_source"] == "kernel-host-mcp" {
			count++
		}
	}
	if count == 0 {
		return compatibilityResolveInputResult{}, false, nil
	}
	if count != len(responses) {
		return compatibilityResolveInputResult{}, true, resolveInputRequestError(http.StatusBadRequest, "MCP host approvals cannot be mixed with other input responses.")
	}
	ownerID, found, err := s.workspaceStore.ProjectOwnerIDContext(ctx, frame.ProjectID)
	if err != nil || !found {
		return compatibilityResolveInputResult{}, true, errors.New("MCP approval owner is unavailable")
	}
	seen := make(map[string]bool, len(responses))
	// Validate the complete batch before committing any decision.
	for _, response := range responses {
		id := strings.TrimSpace(firstNonEmpty(response.ToolID, response.RequestID))
		if id == "" || seen[id] {
			return compatibilityResolveInputResult{}, true, resolveInputRequestError(http.StatusBadRequest, "MCP approval request IDs must be nonempty and unique.")
		}
		seen[id] = true
		item := pendingByID[id]
		entry, found, err := s.runtimeStore.Get(agentRuntimeApprovalNamespace, id)
		if err != nil || !found {
			return compatibilityResolveInputResult{}, true, errors.New("MCP approval authority is unavailable")
		}
		value := mapValue(entry.Value)
		if value["approvalSource"] != "kernel-host-mcp" || value["frameId"] != frame.ID ||
			value["sessionId"] != frame.ID || value["tool"] != item["tool_name"] ||
			value["toolCallId"] != item["tool_call_id"] || item["frame_id"] != frame.ID || item["approval_id"] != id {
			return compatibilityResolveInputResult{}, true, resolveInputRequestError(http.StatusConflict, "MCP approval authority conflicts with the pending task input.")
		}
		if status := stringValue(value["status"]); status != "pending" && status != "approved" && status != "denied" {
			return compatibilityResolveInputResult{}, true, resolveInputRequestError(http.StatusConflict, "MCP approval is no longer active.")
		}
		_, approved, scope, _, err := compatibilityApprovalResolution(response)
		if err != nil {
			return compatibilityResolveInputResult{}, true, resolveInputRequestError(http.StatusBadRequest, err.Error())
		}
		if approved && (scope != "once" && scope != "always" || scope == "always" && !boolValue(value["rememberable"], false)) {
			return compatibilityResolveInputResult{}, true, resolveInputRequestError(http.StatusBadRequest, "MCP approval scope is not supported by this request.")
		}
	}
	resolved := make([]string, 0, len(responses))
	for _, response := range responses {
		id := strings.TrimSpace(firstNonEmpty(response.ToolID, response.RequestID))
		if _, _, err := s.resolveCompatibilityAgentToolApproval(ctx, pendingByID[id], response); err != nil {
			return compatibilityResolveInputResult{}, true, err
		}
		if err := s.workspaceStore.RemoveKernelArtifactApprovalRequest(ctx, ownerID, frame.ProjectID, frame.ID, id); err != nil {
			return compatibilityResolveInputResult{}, true, err
		}
		resolved = append(resolved, id)
	}
	metadata, _, err := s.workspaceStore.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		return compatibilityResolveInputResult{}, true, err
	}
	remaining := make([]string, 0)
	for _, item := range compatibilityServerPendingInputs(metadata.ContextData) {
		remaining = append(remaining, compatibilityServerPendingInputID(item))
	}
	if err := s.publishWebConfirmationRemovals(frame.ID, resolved, frame.Status); err != nil {
		return compatibilityResolveInputResult{}, true, err
	}
	return compatibilityResolveInputResult{Frame: frame, Status: frame.Status, RemainingIDs: remaining}, true, nil
}
