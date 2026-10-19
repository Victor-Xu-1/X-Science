package server

import (
	"errors"
	"net/http"

	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

// Legacy presentation does not create a binding, invent a prepared snapshot,
// or claim that an old worker loaded a newly captured Agent definition.
func (s *Server) legacySessionSpecialistReadback(access workspace.KernelFrameAccess, current sessionagent.Readback) (sessionagent.Readback, error) {
	if current.Revision != 0 || current.Phase != "legacy" {
		return current, nil
	}
	current.Applied = sessionagent.Selection{
		AgentName: access.Frame.AgentName, DisplayName: access.Frame.AgentName,
	}
	current.AppliedUnavailable = true
	record, err := s.webConversationAssistantRecord(access.UserID, access.Frame.ID, access.Frame.AgentName)
	if err != nil {
		var requestErr *webConversationRequestError
		if errors.As(err, &requestErr) && requestErr.Status == http.StatusConflict {
			return current, nil
		}
		return current, err
	}
	defaultRecord, found, err := s.webAssistantRecord(access.UserID, defaultWebConversationAssistantID())
	if err != nil {
		return current, err
	}
	current.Applied = sessionagent.Selection{
		AgentName: access.Frame.AgentName, SelectionName: record.Agent.Name,
		AssistantID: record.RuntimeID, DisplayName: record.Agent.DisplayName,
		IsDefault: found && defaultRecord.Agent.Enabled && record.Agent.Name == defaultRecord.Agent.Name && record.RuntimeID == defaultRecord.RuntimeID,
	}
	current.AppliedUnavailable = !record.Agent.Enabled
	return current, nil
}
