package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

const kernelSpecialistApprovalSource = "kernel-host-specialist-switch"

func (s *Server) requireKernelSpecialistApproval(ctx context.Context, origin kernelAgentSwitchOrigin, access workspace.KernelFrameAccess, command sessionagent.Command, definition sessionagent.Snapshot) (waitErr error) {
	if s.runtimeStore == nil {
		return kernelruntime.NewHostCallError("approval_unavailable", "Agent switch approval is unavailable")
	}
	binding := origin.bound.transcriptExecution
	claimDigest := sha256.Sum256([]byte(binding.claim.ClaimToken))
	input := map[string]any{"command_id": command.CommandID, "desired_agent": command.DesiredAgent, "snapshot_id": definition.ID}
	metadata := map[string]any{"approvalType": "specialist_switch", "frameId": access.Frame.ID, "projectId": access.Frame.ProjectID,
		"sessionId": access.Frame.ID, "branchId": command.Branch.ID, "branchGeneration": command.Branch.Generation,
		"frameIncarnationId": command.Scope.FrameIncarnationID, "rootFrameId": command.Scope.RootFrameID, "rootIncarnationId": command.Scope.RootIncarnationID,
		"streamUid": binding.stream.UID, "streamEpoch": binding.stream.Epoch, "runnerId": binding.claim.RunnerID, "runnerAttempt": binding.claim.Attempt,
		"claimedInputRevision": binding.claim.ClaimedInputRevision, "runnerClaimHash": hex.EncodeToString(claimDigest[:]), "operationId": binding.operationID,
		"kernelId": binding.kernelID, "kernelGeneration": binding.kernelGeneration, "snapshotId": definition.ID}
	defaults := s.agentRuntimeApprovalDefaults()
	defaults.Mode, defaults.RequireReason, defaults.RememberDecisions = "ask", false, false
	permission := s.queueAgentRuntimeApprovalForSourceWithMetadata("host.agents.switch", agentruntime.ToolCall{ID: origin.callID, Name: "host.agents.switch"}, input, "ask", defaults, kernelSpecialistApprovalSource, metadata)
	approvalID := strings.TrimSpace(stringValue(permission["approvalId"]))
	if approvalID == "" {
		return kernelruntime.NewHostCallError("approval_unavailable", "Agent switch approval could not be persisted")
	}
	defer func() {
		if waitErr != nil {
			waitErr = errors.Join(waitErr, s.retirePendingKernelHostApproval(approvalID, "Agent switch wait ended"))
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.workspaceStore.RemoveKernelArtifactApprovalRequest(cleanupCtx, access.UserID, access.Frame.ProjectID, access.Frame.ID, approvalID); err != nil {
			waitErr = errors.Join(waitErr, err)
			return
		}
		waitErr = errors.Join(waitErr, s.publishWebConfirmationRemovals(access.Frame.ID, []string{approvalID}, access.Frame.Status))
	}()
	preview, _ := json.Marshal(map[string]any{"assistant_id": definition.AssistantID, "agent": definition.SelectionName, "snapshot_id": definition.ID, "applies": "next_fresh_user_input"})
	request := map[string]any{"requestId": approvalID, "kind": agentToolApprovalKind, "approval_id": approvalID, "approval_source": kernelSpecialistApprovalSource,
		"frame_id": access.Frame.ID, "tool_call_id": origin.callID, "tool": "host.agents.switch", "tool_name": "host.agents.switch",
		"title": "Switch Agent for the next task input", "description": definition.DisplayName, "target": definition.SelectionName, "code": string(preview), "mode": "rw", "rememberable": false}
	if err := s.workspaceStore.AddKernelArtifactApprovalRequest(ctx, access.UserID, access.Frame.ProjectID, access.Frame.ID, request); err != nil {
		return err
	}
	frame, found, err := s.workspaceStore.GetFrameRealtimeContext(access.Frame.ID)
	if err != nil {
		return err
	}
	if !found {
		return sessionagent.ErrScopeStale
	}
	if err := s.publishWebConfirmationProjection(frame, workspace.FrameEvent{}); err != nil {
		return err
	}
	return s.waitForKernelHostApproval(ctx, approvalID, "Agent switch")
}

// Used by the existing task-input decision endpoint before recording consent.
// It cannot wake a new worker or approve a stale request after restart.
func (s *Server) validateSpecialistApprovalOrigin(ctx context.Context, frame workspace.Frame, value map[string]any) error {
	owner, ownerFound, err := s.workspaceStore.ProjectOwnerIDContext(ctx, frame.ProjectID)
	if err != nil {
		return err
	}
	if !ownerFound {
		return sessionagent.ErrScopeStale
	}
	operation, found, err := s.workspaceStore.GetKernelLocalOperation(ctx, owner, stringValue(value["operationId"]))
	if err != nil {
		return err
	}
	attempt, ok := webPositiveSafeInteger(value["runnerAttempt"])
	epoch, epochOK := webPositiveSafeInteger(value["streamEpoch"])
	inputRevision, inputOK := webPositiveSafeInteger(value["claimedInputRevision"])
	kernelGeneration, kernelOK := webPositiveSafeInteger(value["kernelGeneration"])
	branchGeneration, branchOK := webPositiveSafeInteger(value["branchGeneration"])
	if !found || !ok || !epochOK || !inputOK || !kernelOK || !branchOK || operation.FrameID != frame.ID || operation.ProjectID != frame.ProjectID || operation.OwnerUserID == "" ||
		operation.FrameIncarnationID != value["frameIncarnationId"] || operation.RootFrameID != value["rootFrameId"] || operation.RootFrameIncarnationID != value["rootIncarnationId"] ||
		operation.StreamUID != value["streamUid"] || operation.RunnerAttempt != attempt || operation.RunnerID != value["runnerId"] || operation.RunnerClaimSHA256 != value["runnerClaimHash"] || operation.State != workspace.KernelLocalOperationStateStarted ||
		operation.AdmittedInputRevision <= 0 || operation.AdmittedInputRevision > inputRevision || operation.KernelID != stringValue(value["kernelId"]) || operation.KernelGeneration != kernelGeneration ||
		operation.BranchID != stringValue(value["branchId"]) || operation.BranchGeneration != branchGeneration ||
		stringValue(mapValue(value["input"])["snapshot_id"]) != stringValue(value["snapshotId"]) {
		return sessionagent.ErrScopeStale
	}
	var live bool
	err = s.transcriptStore.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_runner_attempts r JOIN transcript_frame_authority a ON a.active_stream_uid=r.stream_uid
			JOIN transcript_streams st ON st.stream_uid=r.stream_uid AND st.epoch=a.active_epoch AND st.owner_id=a.owner_id
			JOIN transcript_branch_state b ON b.stream_uid=st.stream_uid
			WHERE a.owner_id=? AND a.session_id=? AND a.active_epoch=? AND r.attempt=? AND r.runner_id=? AND r.status='running' AND r.expires_at>?
			AND lower(hex(r.claim_token_sha256))=? AND r.claimed_input_revision=? AND b.active_branch_id=? AND b.generation=?)`, operation.OwnerUserID, frame.ID, epoch, attempt, operation.RunnerID, time.Now().UTC(), operation.RunnerClaimSHA256, inputRevision, operation.BranchID, operation.BranchGeneration).Scan(&live)
	})
	if err != nil {
		return err
	}
	if !live {
		return sessionagent.ErrScopeStale
	}
	return nil
}
