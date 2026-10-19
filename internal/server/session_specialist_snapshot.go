package server

import (
	"strings"

	"x-science/internal/agentruntime"
	"x-science/internal/sessionagent"
	workspace "x-science/internal/persistence/workspace"
)

// captureSessionSpecialist copies a fixed whitelist, never an assistant Defaults
// map, provider configuration, connector environment or credential value.
func (s *Server) captureSessionSpecialist(owner,target string,isDefault bool) (sessionagent.Snapshot,error) {
	record,found,err:=s.webAssistantRecord(owner,target)
	if err!=nil { return sessionagent.Snapshot{},err }
	if !found || !record.Agent.Enabled || record.Agent.Name!=target { return sessionagent.Snapshot{},sessionagent.ErrTargetUnavailable }
	runtimeAgent,err:=s.webAssistantRuntimeAgentForRecord(owner,record)
	if err!=nil { return sessionagent.Snapshot{},err }
	snapshot:=sessionagent.Snapshot{AgentName:runtimeAgent,SelectionName:record.Agent.Name,IsDefault:isDefault,AssistantID:record.RuntimeID,DisplayName:record.Agent.DisplayName,DefinitionID:record.Agent.ID}
	if selectedProfile,found,err:=s.workspaceStore.GetAgent(owner,record.Agent.Name); err!=nil { return snapshot,err } else if found { snapshot.SelectionProfileID=selectedProfile.ID }
	if definition,found:=s.agentCatalog.Agent(runtimeAgent); found {
		snapshot.Bundled=&sessionagent.BundledDefinition{SystemPrompt:definition.EffectiveSystemPrompt(),SkillNames:append([]string(nil),definition.SkillNames...),
			ConnectorIDs:append([]string(nil),definition.ConnectorIDs...),ExcludedTools:append([]string(nil),definition.ExcludedTools...),
			SkillsLocked:definition.SkillsLocked,SupportsPlanMode:definition.SupportsPlanMode,Unrestricted:definition.Unrestricted,
			EnableThinking:definition.EnableThinking,EnableWebSearch:definition.EnableWebSearch,EnableWebFetch:definition.EnableWebFetch,MaxToolResultChars:definition.MaxToolResultChars}
	}
	profile,profileFound,err:=s.workspaceStore.GetAgent(owner,runtimeAgent)
	if err!=nil { return snapshot,err }
	if profileFound {
		if !profile.Enabled { return snapshot,sessionagent.ErrTargetUnavailable }
		snapshot.DefinitionID=profile.ID
		snapshot.Profile=&sessionagent.PersonalDefinition{SystemPrompt:profile.SystemPrompt,SkillNames:append([]string(nil),profile.SkillNames...),
			SkillTombstones:append([]string(nil),profile.SkillTombstones...),ConnectorTombstones:append([]string(nil),profile.ConnectorTombstones...),Unrestricted:profile.Unrestricted}
	}
	settings:=sessionagent.Settings{Model:webAssistantRuntimeScalar(record,nil,"model"),ThoughtLevel:webAssistantRuntimeScalar(record,nil,"thought_level"),
		CustomSkillNames:append([]string(nil),record.Config.CustomSkillNames...),DisabledBuiltinSkills:appendUniqueFolded(record.Agent.SkillTombstones,record.Config.DisabledBuiltinSkills...),
		AllowedSkillNames:append([]string(nil),s.webAssistantAllowedSkillNames(record)...)}
	settings.DefaultSkillNames,settings.DefaultSkillsFixed=webAssistantDefaultSkillSelection(record)
	settings.AllowedMCPIDs,err=s.webAssistantAllowedMCPIDs(owner,record)
	if err!=nil { return snapshot,err }
	settings.AllowedModels,err=s.webAssistantAllowedModels(owner,record)
	if err!=nil { return snapshot,err }
	if prompt,found,err:=s.workspaceStore.GetCustomAgentPrompt(owner,record.Agent.Name); err!=nil { return snapshot,err } else if found { settings.CustomPrompt=prompt.PromptText }
	snapshot.Settings=settings
	return sessionagent.SealSnapshot(snapshot)
}

// These adapters feed the same profile/tool resolution code as ordinary
// sessions. The private claim reader, not JSON session options, sets snapshot.
func frozenBundledAgent(snapshot *sessionagent.Snapshot) (agentruntime.AgentDefinition,bool) {
	if snapshot==nil || snapshot.Bundled==nil { return agentruntime.AgentDefinition{},false }
	b:=snapshot.Bundled
	return agentruntime.AgentDefinition{Name:snapshot.AgentName,Enabled:true,SystemPrompt:b.SystemPrompt,SkillNames:b.SkillNames,ConnectorIDs:b.ConnectorIDs,
		ExcludedTools:b.ExcludedTools,SkillsLocked:b.SkillsLocked,SupportsPlanMode:b.SupportsPlanMode,Unrestricted:b.Unrestricted,
		EnableThinking:b.EnableThinking,EnableWebSearch:b.EnableWebSearch,EnableWebFetch:b.EnableWebFetch,MaxToolResultChars:b.MaxToolResultChars},true
}

func frozenPersonalAgent(snapshot *sessionagent.Snapshot) (workspace.Agent,bool) {
	if snapshot==nil || snapshot.Profile==nil { return workspace.Agent{},false }
	p:=snapshot.Profile
	return workspace.Agent{ID:snapshot.DefinitionID,Name:snapshot.AgentName,Enabled:true,SystemPrompt:p.SystemPrompt,SkillNames:p.SkillNames,
		SkillTombstones:p.SkillTombstones,ConnectorTombstones:p.ConnectorTombstones,Unrestricted:p.Unrestricted},true
}

func frozenSpecialistAssistant(snapshot sessionagent.Snapshot) webAssistantRecord {
	// Validation must use the captured allowlist, including for unrestricted
	// assistants: re-expanding today's mutable catalog would widen this input.
	return webAssistantRecord{Agent:workspace.Agent{ID:snapshot.DefinitionID,Name:snapshot.SelectionName,DisplayName:snapshot.DisplayName,Enabled:true,
		SkillNames:snapshot.Settings.AllowedSkillNames,SkillTombstones:snapshot.Settings.DisabledBuiltinSkills},RuntimeID:snapshot.AssistantID,
		Config:webAssistantStoredConfig{Models:append([]string(nil),snapshot.Settings.AllowedModels...)}}
}

func specialistRuntimeTarget(snapshot *sessionagent.Snapshot,fallback string) string {
	if snapshot!=nil { return snapshot.AgentName }
	return strings.TrimSpace(fallback)
}
