package server

import (
	"context"
	"errors"
	"net/http"

	"x-science/internal/sessionagent"
)

// This is the same default assistant authority as new-conversation creation;
// None in a client is a typed intent, never a guessed Agent or slug.
func defaultWebConversationAssistantID() string { return webAssistantID("OPERON") }

func (s *Server) captureSessionSpecialistPreparation(ctx context.Context,owner,frameID string,command sessionagent.Command,initial bool) (sessionagent.Preparation,error) {
	var result sessionagent.Preparation
	target:=command.DesiredAgent
	if command.UseDefault {
		record,found,err:=s.webAssistantRecord(owner,defaultWebConversationAssistantID())
		if err!=nil { return result,err }
		if !found { return result,sessionagent.ErrTargetUnavailable }
		target=record.Agent.Name
	}
	var err error
	result.Desired,err=s.captureSessionSpecialist(owner,target,command.UseDefault)
	if err!=nil || !initial { return result,err }
	frame,found,err:=s.workspaceStore.GetFrame(frameID)
	if err!=nil { return result,err }
	if !found { return result,sessionagent.ErrTargetUnavailable }
	previous,err:=s.webConversationAssistantRecord(owner,frameID,frame.AgentName)
	var fallback sessionagent.Snapshot
	if err==nil {
		defaultRecord,defaultFound,defaultErr:=s.webAssistantRecord(owner,defaultWebConversationAssistantID())
		if defaultErr!=nil { return result,defaultErr }
		fallback,err=s.captureSessionSpecialist(owner,previous.Agent.Name,defaultFound && defaultRecord.Agent.Enabled && previous.RuntimeID==defaultRecord.RuntimeID)
	}
	if err!=nil {
		var requestErr *webConversationRequestError
		var profileErr *agentProfileRequestError
		unavailable:=errors.Is(err,sessionagent.ErrTargetUnavailable) || errors.As(err,&requestErr) && requestErr.Status==http.StatusConflict || errors.As(err,&profileErr) && profileErr.Status==http.StatusBadRequest
		if !unavailable { return result,err }
		result.FallbackUnavailable=true
		return result,nil
	}
	result.Fallback=&fallback
	// Freeze only the existing conversation's whitelisted non-secret overrides
	// for its future fallback inputs. This is not a claim that an old worker
	// loaded this new snapshot, and it is not a retroactive execution record.
	metadata,found,err:=s.workspaceStore.GetFrameRuntimeMetadata(frameID)
	if err!=nil { return result,err }
	if found {
		assistant,_:=metadata.ContextData["web_assistant"].(map[string]any)
		overrides,_:=assistant["conversation_overrides"].(map[string]any)
		settings:=&result.Fallback.Settings
		settings.Model=webAssistantRuntimeScalar(previous,overrides,"model")
		settings.ThoughtLevel=webAssistantRuntimeScalar(previous,overrides,"thought_level")
		settings.DisabledBuiltinSkills=appendUniqueFolded(settings.DisabledBuiltinSkills,webAssistantStringValues(overrides["disabled_builtin_skill_ids"])...)
		settings.AllowedSkillNames=subtractFoldedStrings(settings.AllowedSkillNames,settings.DisabledBuiltinSkills)
		if value,provided:=overrides["skill_ids"]; provided { settings.DefaultSkillNames=webAssistantStringValues(value); settings.DefaultSkillsFixed=true }
		if err:=validateWebAssistantSkillSelection(settings.DefaultSkillNames,settings.AllowedSkillNames,settings.DisabledBuiltinSkills); err!=nil { return result,err }
	}
	sealed,err:=sessionagent.SealSnapshot(*result.Fallback)
	result.Fallback=&sealed
	return result,err
}
