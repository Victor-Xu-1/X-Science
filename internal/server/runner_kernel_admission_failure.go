package server

import (
	"context"
	"errors"

	workspace "x-science/internal/persistence/workspace"
)

// settleKernelAdmissionFailure derives non-execution only from the durable
// approved (not prepared/started) operation. A launcher/observation error after
// dispatch must keep its existing recovery authority, never become a fabricated
// preflight. The normal atomic preflight settlement owns the resulting receipt.
func (s *Server) settleKernelAdmissionFailure(run *sessionRunnerChatRun, callID string, result map[string]any) (map[string]any, bool, error) {
	if run == nil || run.Transcript == nil || run.KernelOperationIDs[callID] == "" {
		return result, false, nil
	}
	if s == nil || s.workspaceStore == nil {
		return nil, false, errors.New("kernel failure settlement store is unavailable")
	}
	operation, found, err := s.workspaceStore.GetKernelLocalOperation(context.Background(), run.Transcript.Stream.OwnerID, run.KernelOperationIDs[callID])
	if err != nil {
		return nil, false, err
	}
	if !found || operation.ToolCallID != callID || operation.StreamUID != run.Transcript.Stream.UID {
		return nil, false, workspace.ErrKernelLocalOperationConflict
	}
	if operation.State == workspace.KernelLocalOperationStateCancelled && operation.ExecutionID == "" {
		persisted, err := s.kernelLocalOperationPersistedResult(operation)
		if err != nil {
			return nil, false, err
		}
		if agentKernelPreflightResult(persisted) {
			return persisted, true, nil
		}
	}
	if !kernelAdmissionFailureIsUnstarted(operation) {
		return result, false, nil
	}
	value := copyMapAny(result)
	if value == nil {
		value = map[string]any{}
	}
	value["ok"], value["executed"], value["preflight"] = false, false, true
	if stringValue(value["message"]) == "" && stringValue(value["error"]) == "" {
		value["message"] = "Runtime preparation failed before execution."
	}
	if stringValue(value["code"]) == "" && stringValue(value["status"]) == "" {
		value["code"] = "execution_preflight_failed"
	}
	_, handled, err := s.reconcileKernelOperationPreflightResult(context.Background(), run, callID, value)
	return value, handled, err
}

func kernelAdmissionFailureIsUnstarted(operation workspace.KernelLocalOperation) bool {
	return operation.State == workspace.KernelLocalOperationStateApproved && operation.ExecutionID == "" && operation.PreparedAt == nil && operation.StartedAt == nil
}
