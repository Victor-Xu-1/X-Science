package server

import (
	"context"
	"fmt"
	"strings"

	"x-science/internal/sessionagent"
)

func validateFrozenSpecialistMCPSelection(selected, allowed []string) error {
	ceiling := normalizedSkillNameSet(allowed)
	for _, id := range selected {
		if _, found := ceiling[strings.ToLower(strings.TrimSpace(id))]; !found {
			return fmt.Errorf("MCP server %q is outside the captured assistant authority", id)
		}
	}
	return nil
}

// Resolve captured IDs through today's existing owner/connector authority.
// New attachments/configuration cannot widen an accepted claim; revocation,
// current method policy and the exact live tool schema still apply normally.
func (s *Server) frozenSpecialistMCPRuntime(ctx context.Context, userID, frameID string, snapshot sessionagent.Snapshot) (workspaceMCPRuntimeContext, error) {
	result := workspaceMCPRuntimeContext{UserID: userID, AgentName: snapshot.AgentName,
		Excluded: make(map[string]struct{}), ExcludedByConnector: make(map[string]map[string]struct{})}
	for _, id := range snapshot.Settings.AllowedMCPIDs {
		server, found, err := s.workspaceStore.GetMCPServer(id, userID)
		if err != nil {
			return result, err
		}
		var connector workspaceMCPRuntimeConnector
		if found {
			connector = customWorkspaceMCPRuntimeConnector(server)
		} else if s.mcpDirectory != nil {
			live, exists, err := s.mcpDirectory.ResolveRuntimeConnector(ctx, userID, id)
			if err != nil {
				return result, err
			}
			if !exists {
				continue
			}
			connector = workspaceMCPRuntimeConnector{ID: live.ID, Name: live.Name, Source: live.Source, Enabled: !live.Config.Disabled}
		} else {
			continue
		}
		if snapshot.Settings.MCPSourceKinds[id] == "" || snapshot.Settings.MCPSourceKinds[id] != connector.Source {
			return result, sessionagent.ErrTargetUnavailable
		}
		result.Connectors = append(result.Connectors, connector)
	}
	selected, constrained, err := s.webAssistantRuntimeMCPSelection(ctx, userID, frameID)
	if err != nil {
		return result, err
	}
	if constrained {
		selectedSet := normalizedSkillNameSet(selected)
		filtered := result.Connectors[:0]
		for _, connector := range result.Connectors {
			if _, found := selectedSet[strings.ToLower(connector.ID)]; found {
				filtered = append(filtered, connector)
			}
		}
		result.Connectors = filtered
	}
	result.Servers = runtimeCustomServers(result.Connectors)
	_, liveExcluded, err := s.workspaceMCPRuntimeExclusions(userID, snapshot.AgentName, result.Connectors)
	if err != nil {
		return result, err
	}
	for _, connector := range result.Connectors {
		values := appendUniqueFolded(nil, snapshot.Settings.ConnectorToolExclusions[connector.ID]...)
		set := make(map[string]struct{}, len(values)+len(liveExcluded[connector.ID]))
		for _, name := range values {
			set[name] = struct{}{}
			result.Excluded[name] = struct{}{}
		}
		for name := range liveExcluded[connector.ID] {
			set[name] = struct{}{}
			result.Excluded[name] = struct{}{}
		}
		result.ExcludedByConnector[connector.ID] = set
	}
	return result, nil
}
