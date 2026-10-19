package server

import (
	"fmt"
	"strings"

	sessionstore "x-science/internal/persistence/sessions"
	"x-science/internal/sessionagent"
)

// The existing assistant policy resolver has one execution path. Only its
// configuration source changes from mutable metadata to the verified immutable
// claim snapshot. Legacy sessions keep their exact prior source and fences.
func (s *Server) sessionAssistantRuntimeSource(session sessionstore.Session, userID, frameAgent string, snapshot *sessionagent.Snapshot) (webAssistantRecord, map[string]any, string, bool, bool, error) {
	if snapshot != nil {
		if sessionagent.ValidateSnapshot(*snapshot) != nil || snapshot.AgentName != frameAgent {
			return webAssistantRecord{}, nil, "", false, false, sessionagent.ErrScopeStale
		}
		record := frozenSpecialistAssistant(*snapshot)
		record.Bundled = snapshot.Bundled != nil
		settings := snapshot.Settings
		mode := "auto"
		if settings.DefaultSkillsFixed {
			mode = "fixed"
		}
		record.Config.Defaults = map[string]any{"skills": map[string]any{"mode": mode, "value": settings.DefaultSkillNames},
			"model": map[string]any{"mode": "fixed", "value": settings.Model}, "thought_level": map[string]any{"mode": "fixed", "value": settings.ThoughtLevel}}
		overrides := map[string]any{}
		if data, ok := session.Orchestration["inputData"].(map[string]any); ok {
			if model, ok := data["model"].(string); ok {
				overrides["model"] = model
			}
		}
		return record, map[string]any{"conversation_overrides": overrides}, settings.CustomPrompt, settings.CustomPrompt != "", true, nil
	}
	metadata, found, err := s.workspaceStore.GetFrameRuntimeMetadata(session.ID)
	if err != nil || !found {
		return webAssistantRecord{}, nil, "", false, false, err
	}
	assistant, ok := metadata.ContextData["web_assistant"].(map[string]any)
	if !ok {
		return webAssistantRecord{}, nil, "", false, false, nil
	}
	assistantID := strings.TrimSpace(webString(assistant["id"]))
	if assistantID == "" {
		assistantID = webAssistantID(frameAgent)
	}
	record, found, err := s.webAssistantRecord(userID, assistantID)
	if err != nil {
		return record, nil, "", false, false, err
	}
	if !found || !record.Agent.Enabled {
		return record, nil, "", false, false, fmt.Errorf("selected assistant %q is unavailable or disabled", assistantID)
	}
	runtimeAgent, err := s.resolveWebAssistantRuntimeAgent(userID, assistantID)
	if err != nil {
		return record, nil, "", false, false, err
	}
	if normalizeBundledAgentName(runtimeAgent) != normalizeBundledAgentName(frameAgent) {
		return record, nil, "", false, false, fmt.Errorf("assistant runtime agent does not match the conversation authority")
	}
	prompt, promptFound, err := s.workspaceStore.GetCustomAgentPrompt(userID, record.Agent.Name)
	return record, assistant, prompt.PromptText, promptFound, true, err
}
