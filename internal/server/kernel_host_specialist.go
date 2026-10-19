package server

import (
	"context"
	"strconv"
	"strings"

	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

type kernelAgentSwitchOrigin struct { bound kernelHostExecutionIdentity; callID string }

func (s *Server) kernelHostSwitchAgent(ctx context.Context,access workspace.KernelFrameAccess,name string,origins ...kernelAgentSwitchOrigin) (any,error) {
	if err:=ctx.Err(); err!=nil { return nil,err }
	if len(origins)!=1 || strings.TrimSpace(origins[0].callID)=="" { return nil,kernelruntime.NewHostCallError("permission_denied","Agent switching requires its original transcript execution") }
	origin:=origins[0]
	identity,err:=s.validateKernelTranscriptExecution(ctx,origin.bound,access)
	if err!=nil { return nil,err }
	if access.Frame.ID!=access.Frame.RootFrameID || access.Frame.ParentFrameID!="" || access.IsHidden || origin.bound.transcriptExecution.operationID=="" { return nil,kernelruntime.NewHostCallError("permission_denied","Only the visible main execution may request an Agent switch") }
	scope:=sessionagent.Scope{FrameID:access.Frame.ID,FrameIncarnationID:access.Frame.IncarnationID,RootFrameID:access.Frame.RootFrameID,RootIncarnationID:access.RootFrameIncarnationID}
	command:=sessionagent.Command{Schema:sessionagent.CommandSchema,Scope:scope,DesiredAgent:normalizeBundledAgentName(name),
		CommandID:sessionagent.RecordID("host-command",access.UserID,scope.FrameIncarnationID,identity.StreamUID,strconv.FormatInt(identity.RunnerAttempt,10),origin.bound.transcriptExecution.operationID,origin.callID)}
	if result,replayed,err:=s.workspaceStore.ReplaySessionSpecialist(ctx,access.UserID,access.Frame.ID,command,true); err!=nil { return nil,specialistHostError(err) } else if replayed { return specialistHostResult(result),nil }
	main,err:=s.kernelSpecialistMainRole(ctx,origin.bound,access)
	if err!=nil { return nil,err }
	if !main { return nil,kernelruntime.NewHostCallError("permission_denied","The current specialist cannot switch the main Agent binding") }
	state,err:=s.workspaceStore.SessionSpecialist(ctx,access.UserID,access.Frame.ID)
	if err!=nil { return nil,specialistHostError(err) }
	command.ExpectedRevision,command.Branch=state.Revision,state.Branch
	prepared,err:=s.captureSessionSpecialistPreparation(ctx,access.UserID,access.Frame.ID,command,state.Revision==0)
	if err!=nil { return nil,specialistHostError(err) }
	if err:=s.requireKernelSpecialistApproval(ctx,origin,access,command,prepared.Desired); err!=nil { return nil,err }
	// The approval is for this precise configuration, not a same-name target
	// that may have been edited while the user was reading the confirmation.
	confirmed,err:=s.captureSessionSpecialist(access.UserID,command.DesiredAgent,false)
	if err!=nil || confirmed.ID!=prepared.Desired.ID { return nil,kernelruntime.NewHostCallError("selection_stale","The approved Agent definition changed; choose it again") }
	result,err:=s.workspaceStore.PrepareSessionSpecialistHost(ctx,access.UserID,access.Frame.ID,command,prepared,func(tx *transcriptstore.ImmediateTransaction)error {
		live,err:=tx.ValidateLiveRunnerClaim(ctx,identity.RunnerClaim)
		if err!=nil { return err }
		if live.UID!=identity.StreamUID || live.Epoch!=identity.StreamEpoch || origin.bound.transcriptExecution.currentKernelGeneration()==0 || origin.bound.transcriptExecution.currentKernelGeneration()!=origin.bound.transcriptExecution.kernelGeneration { return transcriptstore.ErrClaimStale }
		return nil
	})
	if err!=nil { return nil,specialistHostError(err) }
	return specialistHostResult(result),nil
}

func (s *Server) kernelSpecialistMainRole(ctx context.Context,bound kernelHostExecutionIdentity,access workspace.KernelFrameAccess) (bool,error) {
	snapshot,err:=s.transcriptStore.ClaimedRunnerAgentSnapshot(ctx,bound.transcriptExecution.claim)
	if err!=nil { return false,err }
	if snapshot!=nil { return snapshot.IsDefault,nil }
	// Legacy has no historical definition snapshot. Use only the server's
	// actual default assistant ownership, never an Agent-name allowlist.
	record,err:=s.webConversationAssistantRecord(access.UserID,access.Frame.ID,access.Frame.AgentName)
	if err!=nil { return false,nil }
	defaultRecord,found,err:=s.webAssistantRecord(access.UserID,defaultWebConversationAssistantID())
	return err==nil && found && defaultRecord.Agent.Enabled && record.RuntimeID==defaultRecord.RuntimeID,err
}

func specialistHostResult(result sessionagent.Mutation) map[string]any {
	return map[string]any{"switched":false,"status":result.Snapshot.Phase,"applies":"next_fresh_user_input","receipt":result.Receipt,"snapshot":result.Snapshot,"replayed":result.Replayed}
}

func specialistHostError(err error) error {
	return kernelruntime.NewHostCallError("selection_unavailable","The Agent selection could not be prepared; refresh its current state")
}
