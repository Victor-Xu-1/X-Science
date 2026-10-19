package server

import (
	"context"
	"errors"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

type specialistApprovalUserContextKey struct{}

type specialistApprovalUser struct {
	OwnerID, FrameID, ApprovalID string
}

// The existing authenticated pending-input owner supplies this private scope.
// Neither a model message nor an approval's own JSON can manufacture consent.
func (s *Server) validateSpecialistUserConsent(ctx context.Context, approvalID string, value, message map[string]any) error {
	user, _ := ctx.Value(specialistApprovalUserContextKey{}).(specialistApprovalUser)
	if user.OwnerID == "" || user.FrameID != stringValue(value["frameId"]) || user.ApprovalID != approvalID {
		return errors.New("specialist switching approval requires an authenticated user response")
	}
	if approvalMessageRememberDecision(message) {
		return errors.New("specialist switching approval is valid for this invocation only")
	}
	frame, found, err := s.workspaceStore.GetFrameRealtimeContext(user.FrameID)
	if err != nil {
		return err
	}
	if !found || frame.UserID != user.OwnerID || frame.Frame.ProjectID != stringValue(value["projectId"]) {
		return errors.New("specialist switching approval owner changed")
	}
	approved, explicit := approvalMessageDecision(message)
	if approved && explicit {
		return s.validateSpecialistApprovalOrigin(ctx, frame.Frame, value)
	}
	// A denial grants no authority and can retire a stale original invocation.
	return nil
}

func (s *Server) resolveKernelSpecialistApprovalInputs(
	ctx context.Context,
	frame workspace.CompatibilityFrame,
	pending map[string]map[string]any,
	responses []compatibilityInputResponse,
) (compatibilityResolveInputResult, bool, error) {
	count := 0
	seen := make(map[string]bool, len(responses))
	for _, response := range responses {
		id := strings.TrimSpace(firstNonEmpty(response.ToolID, response.RequestID))
		if id != "" && !seen[id] && stringValue(pending[id]["approval_source"]) == kernelSpecialistApprovalSource {
			count++
		}
		seen[id] = true
	}
	if count == 0 {
		return compatibilityResolveInputResult{}, false, nil
	}
	if count != len(responses) {
		return compatibilityResolveInputResult{}, true, resolveInputRequestError(400, "Specialist approvals cannot be duplicated or mixed with other input responses.")
	}
	owner, found, err := s.workspaceStore.ProjectOwnerIDContext(ctx, frame.ProjectID)
	if err != nil || !found || s.runtimeStore == nil {
		return compatibilityResolveInputResult{}, true, errors.New("specialist approval owner is unavailable")
	}
	type decision struct {
		id       string
		approved bool
	}
	decisions := make([]decision, 0, count)
	for _, response := range responses {
		id := strings.TrimSpace(firstNonEmpty(response.ToolID, response.RequestID))
		_, approved, scope, _, err := compatibilityApprovalResolution(response)
		if err != nil {
			return compatibilityResolveInputResult{}, true, err
		}
		// The generic parser's omitted-scope default is conversation. The actual
		// public one-shot control omits scope, so reject only explicit broadening.
		if response.Scope != "" && scope != "once" || strings.TrimSpace(response.Action) == "allow_always" {
			return compatibilityResolveInputResult{}, true, resolveInputRequestError(400, "Specialist approval is valid for this invocation only.")
		}
		entry, found, err := s.runtimeStore.Get(agentRuntimeApprovalNamespace, id)
		if err != nil || !found {
			return compatibilityResolveInputResult{}, true, errors.New("specialist approval request is unavailable")
		}
		value := mapValue(entry.Value)
		if stringValue(value["approvalSource"]) != kernelSpecialistApprovalSource ||
			stringValue(value["frameId"]) != frame.ID || stringValue(value["projectId"]) != frame.ProjectID ||
			stringValue(value["toolCallId"]) != stringValue(pending[id]["tool_call_id"]) {
			return compatibilityResolveInputResult{}, true, errors.New("specialist approval authority changed")
		}
		decisions = append(decisions, decision{id, approved})
	}
	ids := make([]string, 0, count)
	for _, decision := range decisions {
		userCtx := context.WithValue(ctx, specialistApprovalUserContextKey{}, specialistApprovalUser{owner, frame.ID, decision.id})
		if _, err := s.resolveAgentRuntimeApprovalMessage(userCtx, "", map[string]any{
			"approvalId": decision.id, "approve": decision.approved, "remember": false,
		}); err != nil {
			return compatibilityResolveInputResult{}, true, err
		}
		if err := s.workspaceStore.RemoveKernelArtifactApprovalRequest(ctx, owner, frame.ProjectID, frame.ID, decision.id); err != nil {
			return compatibilityResolveInputResult{}, true, err
		}
		ids = append(ids, decision.id)
	}
	metadata, _, err := s.workspaceStore.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		return compatibilityResolveInputResult{}, true, err
	}
	remaining := make([]string, 0)
	for _, item := range compatibilityServerPendingInputs(metadata.ContextData) {
		if id := compatibilityServerPendingInputID(item); id != "" {
			remaining = append(remaining, id)
		}
	}
	if err := s.publishWebConfirmationRemovals(frame.ID, ids, frame.Status); err != nil {
		return compatibilityResolveInputResult{}, true, err
	}
	return compatibilityResolveInputResult{Frame: frame, Status: frame.Status, RemainingIDs: remaining}, true, nil
}
