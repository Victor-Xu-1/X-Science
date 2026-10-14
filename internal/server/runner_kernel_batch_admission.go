package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"x-science/internal/agentruntime"
	"x-science/internal/kernelcontract"
	workspace "x-science/internal/persistence/workspace"
)

type kernelBatchStartPreparation struct {
	Witness workspace.KernelBatchAdmission
	Input   json.RawMessage
}

func (s *Server) prepareKernelBatchStartAdmission(ctx context.Context, options SessionRunnerChatOptions, run *sessionRunnerChatRun, callID string, details []map[string]any) (*kernelBatchStartPreparation, error) {
	if run == nil || run.Transcript == nil || s.workspaceStore == nil || len(details) == 0 {
		return nil, nil
	}
	batchID := run.ToolBatchIDs[callID]
	ordinal, exists := run.ToolBatchOrdinals[callID]
	name, err := canonicalRuntimeToolName(stringValue(details[0]["toolName"]))
	if err != nil || !kernelcontract.IsTool(name) || batchID == "" || !exists {
		return nil, nil
	}
	state, err := s.workspaceStore.KernelToolCallBatchAdmission(ctx, run.Transcript.Claim, batchID, ordinal)
	if err != nil {
		return nil, err
	}
	if !state.Deferred {
		return nil, nil
	}
	if state.Item.ToolCallID != callID || state.Item.ToolName != name {
		return nil, workspace.ErrKernelLocalOperationConflict
	}
	if state.OperationFound {
		if run.KernelOperationIDs == nil {
			run.KernelOperationIDs = map[string]string{}
		}
		run.KernelOperationIDs[callID] = state.Operation.OperationID
		return nil, nil
	}
	// Schema-invalid items remain ordinary rejected tool calls, not executable
	// kernel proposals. Their normal validation receipt still advances the batch.
	if !kernelcontract.Admitted(name, state.Item.ArgumentsJSON) {
		return nil, nil
	}
	gateway, err := s.kernelCheckpointAdmissionGateway(ctx, options, run, name, nil)
	if err != nil {
		return nil, err
	}
	arguments, err := normalizedKernelCheckpointArguments(gateway, name, state.Item.ArgumentsJSON)
	if err != nil {
		return nil, err
	}
	canonical, _, err := kernelcontract.CanonicalInput(name, arguments)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	return &kernelBatchStartPreparation{Input: canonical, Witness: workspace.KernelBatchAdmission{Version: workspace.KernelBatchAdmissionVersion,
		BatchID: batchID, Ordinal: ordinal, ProposalSHA256: state.Item.ArgumentsSHA256, InputSHA256: hex.EncodeToString(digest[:]), InputByteLength: len(canonical)}}, nil
}

// admittedKernelBatchInput resolves an execution from its immutable start
// witness. The original proposal is accepted only by exact canonical equality;
// model/context changes cannot replace already admitted executable parameters.
func (g serverAgentRuntimeToolGateway) admittedKernelBatchInput(ctx context.Context, call agentruntime.ToolCall) (map[string]any, bool, error) {
	if g.server == nil || g.server.workspaceStore == nil || g.taskRun == nil || g.taskRun.Transcript == nil || !kernelcontract.IsTool(call.Name) {
		return nil, false, nil
	}
	run := g.taskRun
	batchID := run.ToolBatchIDs[call.ID]
	ordinal, exists := run.ToolBatchOrdinals[call.ID]
	if batchID == "" || !exists {
		return nil, false, nil
	}
	state, err := g.server.workspaceStore.KernelToolCallBatchAdmission(ctx, run.Transcript.Claim, batchID, ordinal)
	if err != nil {
		return nil, true, err
	}
	if !state.Deferred {
		return nil, false, nil
	}
	if !state.OperationFound {
		if !kernelcontract.Admitted(state.Item.ToolName, state.Item.ArgumentsJSON) {
			return nil, false, nil
		}
		return nil, true, errors.New("kernel input has not been admitted at its ordered start boundary")
	}
	proposal, _ := canonicalKernelCheckpointArguments(call.Name, call.Arguments)
	canonical, _, err := kernelcontract.CanonicalInput(call.Name, proposal)
	if err != nil || state.Item.ToolCallID != call.ID || state.Item.ToolName != call.Name ||
		(!bytes.Equal(canonical, state.Item.ArgumentsJSON) && !bytes.Equal(canonical, state.Operation.InputJSON)) {
		return nil, true, workspace.ErrKernelLocalOperationConflict
	}
	var input map[string]any
	if json.Unmarshal(state.Operation.InputJSON, &input) != nil || input == nil {
		return nil, true, workspace.ErrKernelLocalOperationConflict
	}
	return input, true, nil
}
