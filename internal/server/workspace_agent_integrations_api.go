package server

import (
	"net/http"
	"net/url"

	workspace "synon-go/internal/persistence/workspace"
)

func (s *Server) handleWorkspaceAgentPrompt(w http.ResponseWriter, r *http.Request, store *workspace.Store, agentName string) {
	userID, ok := workspaceScopedUserID(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		prompt, found, err := store.GetCustomAgentPrompt(userID, agentName)
		if err != nil {
			writeWorkspaceJSON(w, workspaceStatus(err), map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if !found {
			writeWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true, "prompt": nil})
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true, "prompt": prompt})
	case http.MethodPut:
		var input struct {
			UserID     string `json:"userId"`
			PromptText string `json:"promptText"`
		}
		if err := decodeWorkspaceJSON(r, &input); err != nil {
			writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		ownerID, ok := workspaceClaimedUserID(w, userID, input.UserID)
		if !ok {
			return
		}
		prompt, err := store.UpsertCustomAgentPrompt(ownerID, agentName, input.PromptText)
		if err != nil {
			writeWorkspaceJSON(w, workspaceStatus(err), map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true, "prompt": prompt})
	case http.MethodDelete:
		if err := store.DeleteCustomAgentPrompt(userID, agentName); err != nil {
			writeWorkspaceJSON(w, workspaceStatus(err), map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
	}
}

func (s *Server) handleWorkspaceAgentConnectors(w http.ResponseWriter, r *http.Request, store *workspace.Store, agentName string, segments []string) {
	userID, ok := workspaceScopedUserID(w, r)
	if !ok {
		return
	}
	if len(segments) == 2 && r.Method == http.MethodGet {
		connectors, err := store.ListAgentConnectors(userID, agentName)
		if err != nil {
			writeWorkspaceJSON(w, workspaceStatus(err), map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true, "connectors": connectors})
		return
	}
	if len(segments) != 3 || (r.Method != http.MethodPost && r.Method != http.MethodDelete) {
		writeWorkspaceJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "workspace endpoint not found"})
		return
	}
	connectorID, err := url.PathUnescape(segments[2])
	if err != nil {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid connector id"})
		return
	}
	if r.Method == http.MethodDelete {
		if err := store.UnassignMCPServerFromAgent(userID, agentName, connectorID); err != nil {
			writeWorkspaceJSON(w, workspaceStatus(err), map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if _, err := s.publishUserEvent(userID, "connector_update", map[string]any{"connector_id": connectorID, "agent_name": agentName, "action": "detached"}); err != nil {
			writeDomainEventError(w, "agent connector detach", err)
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	var input struct {
		ID     string `json:"id"`
		UserID string `json:"userId"`
	}
	if err := decodeWorkspaceJSON(r, &input); err != nil {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ownerID, ok := workspaceClaimedUserID(w, userID, input.UserID)
	if !ok {
		return
	}
	assignment, err := store.AssignMCPServerToAgent(workspace.MCPAssignmentInput{
		ID: input.ID, MCPServerID: connectorID, UserID: ownerID, AgentName: agentName,
	})
	if err != nil {
		writeWorkspaceJSON(w, workspaceStatus(err), map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if _, err := s.publishUserEvent(assignment.UserID, "connector_update", map[string]any{"connector_id": connectorID, "agent_name": agentName, "action": "assigned"}); err != nil {
		writeDomainEventError(w, "agent connector attach", err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true, "assignment": assignment})
}

func (s *Server) handleWorkspaceAgentConnectorExclusions(w http.ResponseWriter, r *http.Request, store *workspace.Store, agentName string) {
	userID, ok := workspaceScopedUserID(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		exclusions, err := store.GetAgentConnectorExclusions(userID, agentName)
		if err != nil {
			writeWorkspaceJSON(w, workspaceStatus(err), map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true, "connectorIds": exclusions})
	case http.MethodPut:
		var input struct {
			UserID       string   `json:"userId"`
			ConnectorIDs []string `json:"connectorIds"`
		}
		if err := decodeWorkspaceJSON(r, &input); err != nil {
			writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		ownerID, ok := workspaceClaimedUserID(w, userID, input.UserID)
		if !ok {
			return
		}
		exclusions, err := store.SetAgentConnectorExclusions(ownerID, agentName, input.ConnectorIDs)
		if err != nil {
			writeWorkspaceJSON(w, workspaceStatus(err), map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if _, err := s.publishUserEvent(ownerID, "connector_update", map[string]any{"agent_name": agentName, "connector_ids": exclusions, "action": "exclusions_updated"}); err != nil {
			writeDomainEventError(w, "agent connector exclusions", err)
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, map[string]any{"ok": true, "connectorIds": exclusions})
	default:
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
	}
}
