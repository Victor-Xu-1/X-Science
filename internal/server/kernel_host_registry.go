package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

var kernelHostRegistryMethods = []string{
	"host.skills.list",
	"host.skills.read",
	"host.skills.validate",
	"host.skills.edit",
	"host.skills.install",
	"host.skills.publish",
	"host.skills.delete",
	"host.agents.list",
	"host.agents.create",
	"host.agents.update",
	"host.agents.delete",
	"host.agents.attach_skill",
	"host.agents.detach_skill",
	"host.agents.attach_connector",
	"host.agents.detach_connector",
	"host.agents.list_connectors",
	"host.agents.switch",
}

func isKernelRegistryHostMethod(method string) bool {
	for _, candidate := range kernelHostRegistryMethods {
		if method == candidate {
			return true
		}
	}
	return false
}

func isKernelSkillHostMethod(method string) bool {
	return strings.HasPrefix(method, "host.skills.")
}

func isKernelAgentHostMethod(method string) bool {
	return strings.HasPrefix(method, "host.agents.")
}

func (s *Server) handleKernelRegistryHostCall(
	ctx context.Context,
	access workspace.KernelFrameAccess,
	callID string,
	method string,
	args []any,
	kwargs map[string]any,
	execution ...kernelHostExecutionIdentity,
) (any, error) {
	if isKernelSkillHostMethod(method) {
		return s.handleKernelSkillHostCall(ctx, access, callID, method, args, kwargs)
	}
	if len(execution) == 1 {
		return s.handleKernelAgentHostCall(ctx, access, method, args, kwargs, kernelAgentSwitchOrigin{execution[0], callID})
	}
	return s.handleKernelAgentHostCall(ctx, access, method, args, kwargs)
}

func (s *Server) handleKernelSkillHostCall(
	ctx context.Context,
	access workspace.KernelFrameAccess,
	callID string,
	method string,
	args []any,
	kwargs map[string]any,
	execution ...kernelHostExecutionIdentity,
) (any, error) {
	if s == nil || s.skillCatalog == nil || strings.TrimSpace(s.fileRoot) == "" {
		return nil, kernelruntime.NewHostCallError("unavailable", "personal Skill registry is not configured")
	}
	switch method {
	case "host.skills.list":
		if err := kernelHostExpectNoArguments(args, kwargs, method); err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		return s.kernelHostListSkills()
	case "host.skills.read":
		name, path, err := kernelHostSkillReadInput(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		return s.kernelHostReadSkill(name, path)
	case "host.skills.validate":
		name, err := kernelHostSingleNameInput(args, kwargs, method)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		result, err := s.kernelHostValidateSkill(name)
		return result, kernelHostRegistryError(err)
	case "host.skills.edit":
		name, path, content, oldString, err := kernelHostSkillEditInput(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		s.skillMutationMu.Lock()
		defer s.skillMutationMu.Unlock()
		if len(execution) != 1 {
			return nil, kernelruntime.NewHostCallError("permission_denied", "Skill mutation execution authority is unavailable")
		}
		commit, err := s.kernelSkillMutationCommit(ctx, execution[0], access)
		if err != nil {
			return nil, err
		}
		action, relativePath, err := s.kernelHostEditSkill(ctx, name, path, oldString, content, commit)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		draft, found, err := s.findCompatibilitySkillDraft(name)
		if err != nil || !found {
			return nil, kernelruntime.NewHostCallError("storage_error", "Edited Skill draft is unavailable")
		}
		draftPath, err := filepath.Rel(s.fileRoot, filepath.Dir(draft.Path))
		if err != nil || !filepath.IsLocal(draftPath) {
			return nil, kernelruntime.NewHostCallError("storage_error", "Edited Skill draft path is unavailable")
		}
		return map[string]any{"action": action, "path": relativePath, "draft_path": filepath.ToSlash(draftPath)}, nil
	case "host.skills.install":
		repo, selected, sha, err := kernelHostSkillInstallInput(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		if len(execution) != 1 {
			return nil, kernelruntime.NewHostCallError("permission_denied", "Skill mutation execution authority is unavailable")
		}
		commit, err := s.kernelSkillMutationCommit(ctx, execution[0], access)
		if err != nil {
			return nil, err
		}
		return s.kernelHostInstallMarketplaceSkill(ctx, access, callID, repo, selected, sha, commit)
	case "host.skills.publish":
		name, overwrite, err := kernelHostSkillPublishInput(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		s.skillMutationMu.Lock()
		defer s.skillMutationMu.Unlock()
		if len(execution) != 1 {
			return nil, kernelruntime.NewHostCallError("permission_denied", "Skill mutation execution authority is unavailable")
		}
		commit, err := s.kernelSkillMutationCommit(ctx, execution[0], access)
		if err != nil {
			return nil, err
		}
		skill, err := s.publishCompatibilitySkillDraftWithCommit(name, overwrite, commit)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		return map[string]any{
			"status": "published", "skill_id": "local:" + skill.Name, "name": skill.Name,
			"note": "Skill is now available to the personal runtime catalog.",
		}, nil
	case "host.skills.delete":
		name, err := kernelHostSingleNameInput(args, kwargs, method)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		s.skillMutationMu.Lock()
		defer s.skillMutationMu.Unlock()
		if len(execution) != 1 {
			return nil, kernelruntime.NewHostCallError("permission_denied", "Skill mutation execution authority is unavailable")
		}
		commit, err := s.kernelSkillMutationCommit(ctx, execution[0], access)
		if err != nil {
			return nil, err
		}
		_, draftOnly, err := s.findCompatibilitySkillDraft(name)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		if err := s.deleteCompatibilitySkillWithCommit(access.UserID, name, draftOnly, commit); err != nil {
			return nil, kernelHostRegistryError(err)
		}
		return map[string]any{"deleted": name, "unpublished": !draftOnly}, nil
	default:
		return nil, kernelruntime.NewHostCallError("method_not_allowed", "host Skill method is not allowed")
	}
}

func (s *Server) kernelHostListSkills() ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(s.skillCatalog.Skills()))
	for _, skill := range s.skillCatalog.Skills() {
		if !strings.HasPrefix(skill.Path, "builtin:") && unpublishedManagedSkillPath(skill.Path, s.fileRoot) {
			continue
		}
		origin := s.compatibilitySkillSource(skill)
		items = append(items, map[string]any{
			"name": skill.Name, "origin": origin, "description": skill.Description,
		})
	}
	drafts, err := s.compatibilitySkillDraftNames()
	if err != nil {
		return nil, kernelHostRegistryError(err)
	}
	for _, name := range drafts {
		items = append(items, map[string]any{"name": name, "origin": "draft", "description": ""})
	}
	sort.SliceStable(items, func(i, j int) bool { return stringValue(items[i]["name"]) < stringValue(items[j]["name"]) })
	return items, nil
}

func (s *Server) kernelHostEditSkill(ctx context.Context, name, relativePath, oldString, content string, commit skillDraftCommit) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	_, found, err := s.compatibilityAuthorSkill(name)
	if err != nil {
		return "", "", err
	}
	if found {
		action, _, err := s.editCompatibilitySkillFileWithCommit(name, relativePath, oldString, content, commit)
		if err != nil {
			return "", "", err
		}
		return action, filepath.ToSlash(filepath.Clean(relativePath)), nil
	}
	if oldString != "" {
		return "", "", errors.New("new Skill resources require an empty old_string")
	}
	_, err = s.createCompatibilitySkillDraft(name, relativePath, content, nil, commit)
	if err != nil {
		return "", "", err
	}
	return "created", filepath.ToSlash(filepath.Clean(relativePath)), nil
}

func (s *Server) handleKernelAgentHostCall(
	ctx context.Context,
	access workspace.KernelFrameAccess,
	method string,
	args []any,
	kwargs map[string]any,
	origin ...kernelAgentSwitchOrigin,
) (any, error) {
	if s == nil || s.workspaceStore == nil {
		return nil, kernelruntime.NewHostCallError("unavailable", "agent registry is not configured")
	}
	userID := strings.TrimSpace(access.UserID)
	switch method {
	case "host.agents.list":
		if err := kernelHostExpectNoArguments(args, kwargs, method); err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		return s.kernelHostListAgents(ctx, userID)
	case "host.agents.create":
		input, err := kernelHostAgentCreateInput(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		if input.Unrestricted == nil && input.SkillNames == nil {
			fullAccess := true
			input.Unrestricted = &fullAccess
		}
		agent, _, err := s.createAgentProfile(s.workspaceStore, userID, input)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		return s.kernelHostAgentProjection(agent), nil
	case "host.agents.update":
		name, patch, err := kernelHostAgentUpdateInput(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		agent, err := s.ensureAgentProfile(s.workspaceStore, userID, name)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		updated, _, err := s.updateAgentProfile(s.workspaceStore, userID, agent, patch)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		return s.kernelHostAgentProjection(updated), nil
	case "host.agents.delete":
		name, err := kernelHostSingleNameInput(args, kwargs, method)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		agent, err := s.ensureAgentProfile(s.workspaceStore, userID, name)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		if err := deleteAgentProfile(s.workspaceStore, userID, agent); err != nil {
			return nil, kernelHostRegistryError(err)
		}
		return map[string]any{"deleted": agent.Name}, nil
	case "host.agents.attach_skill", "host.agents.detach_skill":
		name, skillName, err := kernelHostAgentSkillInput(args, kwargs, method)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		if method == "host.agents.attach_skill" {
			if err := s.validateAgentSkillNames([]string{skillName}); err != nil {
				return nil, kernelHostRegistryError(err)
			}
			agent, err := s.workspaceStore.AddAgentSkill(userID, name, skillName)
			if err != nil {
				return nil, kernelHostRegistryError(err)
			}
			return s.kernelHostAgentProjection(agent), nil
		}
		agent, err := s.workspaceStore.RemoveAgentSkill(userID, name, skillName)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		return s.kernelHostAgentProjection(agent), nil
	case "host.agents.attach_connector", "host.agents.detach_connector":
		name, connectorID, include, exclude, err := kernelHostAgentConnectorInput(args, kwargs, method)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		if method == "host.agents.attach_connector" {
			if err := s.kernelHostAttachAgentConnector(ctx, userID, name, connectorID, include, exclude); err != nil {
				return nil, kernelHostRegistryError(err)
			}
		} else if err := s.kernelHostDetachAgentConnector(userID, name, connectorID); err != nil {
			return nil, kernelHostRegistryError(err)
		}
		agent, err := s.ensureAgentProfile(s.workspaceStore, userID, name)
		if err != nil {
			return nil, kernelHostRegistryError(err)
		}
		return s.kernelHostAgentProjection(agent), nil
	case "host.agents.list_connectors":
		connectorName, err := kernelHostOptionalStringInput(args, kwargs, "connector")
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		return s.kernelHostListConnectors(ctx, userID, connectorName)
	case "host.agents.switch":
		name, err := kernelHostSpecialistTargetInput(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		return s.kernelHostSwitchAgent(ctx, access, name, origin...)
	default:
		return nil, kernelruntime.NewHostCallError("method_not_allowed", "host agent method is not allowed")
	}
}

func (s *Server) kernelHostListAgents(ctx context.Context, userID string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records, err := s.webAssistantRecords(userID)
	if err != nil {
		return nil, kernelHostRegistryError(err)
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		source := record.Source
		if source == "user" {
			source = "personal"
		} else if source == "builtin" {
			source = "x_science_llm"
		}
		items = append(items, s.kernelHostAgentProjectionWithSource(record.Agent, source))
	}
	return items, nil
}

func (s *Server) kernelHostAgentProjection(agent workspace.Agent) map[string]any {
	return s.kernelHostAgentProjectionWithSource(agent, "personal")
}

func (s *Server) kernelHostAgentProjectionWithSource(agent workspace.Agent, source string) map[string]any {
	skillNames := any(append([]string{}, agent.SkillNames...))
	if agent.Unrestricted {
		skillNames = nil
	}
	connectors := []string{}
	excludedTools := []string{}
	if s != nil && s.workspaceStore != nil && agent.UserID != "" {
		if attachments, err := s.workspaceStore.ListAgentConnectorAttachments(agent.UserID, agent.Name); err == nil {
			for _, attachment := range attachments {
				connectors = append(connectors, attachment.ServerID)
			}
		}
		if values, err := s.workspaceStore.GetAgentConnectorToolExclusions(agent.UserID, agent.Name); err == nil {
			excludedTools = values
		}
	}
	sort.Strings(connectors)
	return map[string]any{
		"name": agent.Name, "displayName": agent.DisplayName, "description": agent.Description,
		"source": source, "enabled": agent.Enabled, "systemPrompt": agent.SystemPrompt,
		"iconKey": agent.IconKey, "colorKey": agent.ColorKey, "skillNames": skillNames,
		"unrestricted": agent.Unrestricted, "connectors": connectors, "excludedTools": excludedTools,
	}
}

func (s *Server) kernelHostAttachAgentConnector(ctx context.Context, userID, agentName, connectorID, include, exclude string) error {
	agent, err := s.ensureAgentProfile(s.workspaceStore, userID, agentName)
	if err != nil {
		return err
	}
	source := ""
	if _, found, err := s.workspaceStore.GetMCPServer(connectorID, userID); err != nil {
		return err
	} else if found {
		source = "custom"
	} else if s.mcpDirectory != nil {
		connector, found, err := s.mcpDirectory.ResolveRuntimeConnector(ctx, userID, connectorID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("connector %q not found", connectorID)
		}
		source = connector.Source
	} else {
		return errors.New("MCP connector directory is not configured")
	}
	if source == "custom" {
		if _, err := s.workspaceStore.AssignMCPServerToAgent(workspace.MCPAssignmentInput{
			ID: uuid.NewString(), MCPServerID: connectorID, UserID: userID, AgentName: agent.Name,
		}); err != nil {
			return err
		}
	}
	if err := s.workspaceStore.AttachAgentConnector(userID, agent.Name, connectorID, source); err != nil {
		return err
	}
	if _, err := s.workspaceStore.SetAgentConnectorTombstone(userID, agent.Name, connectorID, false); err != nil {
		return err
	}
	if strings.TrimSpace(include) == "" && strings.TrimSpace(exclude) == "" {
		return nil
	}
	if s.mcpDirectory == nil {
		return errors.New("tool filters require MCP connector discovery")
	}
	tools, err := s.mcpDirectory.ListUnifiedConnectorTools(ctx, userID, connectorID)
	if err != nil {
		return err
	}
	includeRE, err := compileOptionalKernelPattern(include, "include_tools_pattern")
	if err != nil {
		return err
	}
	excludeRE, err := compileOptionalKernelPattern(exclude, "exclude_tools_pattern")
	if err != nil {
		return err
	}
	excluded := make([]string, 0)
	for _, tool := range tools {
		if (includeRE != nil && !includeRE.MatchString(tool.Name)) || (excludeRE != nil && excludeRE.MatchString(tool.Name)) {
			excluded = append(excluded, tool.Name)
		}
	}
	_, err = s.workspaceStore.SetAgentConnectorToolExclusions(userID, agent.Name, connectorID, excluded)
	return err
}

func (s *Server) kernelHostDetachAgentConnector(userID, agentName, connectorID string) error {
	agent, err := s.ensureAgentProfile(s.workspaceStore, userID, agentName)
	if err != nil {
		return err
	}
	if _, err := s.workspaceStore.DetachMCPServerFromAgent(userID, agent.Name, connectorID); err != nil {
		return err
	}
	_, err = s.workspaceStore.SetAgentConnectorTombstone(userID, agent.Name, connectorID, true)
	return err
}

func (s *Server) kernelHostListConnectors(ctx context.Context, userID, requested string) (any, error) {
	if s.mcpDirectory == nil {
		return nil, kernelruntime.NewHostCallError("unavailable", "MCP connector directory is not configured")
	}
	connectors, err := s.mcpDirectory.ListUnifiedConnectors(ctx, userID)
	if err != nil {
		return nil, kernelHostRegistryError(err)
	}
	items := make([]map[string]any, 0, len(connectors))
	for _, connector := range connectors {
		if requested != "" && !strings.EqualFold(requested, connector.ID) && !strings.EqualFold(requested, connector.Name) {
			continue
		}
		items = append(items, map[string]any{
			"name": connector.ID, "displayName": connector.DisplayName, "source": connector.Source,
			"description": connector.Description, "authState": connector.AuthState,
			"attachedAgents": connector.AttachedAgents, "enabled": connector.Enabled,
		})
	}
	if requested == "" {
		return items, nil
	}
	if len(items) != 1 {
		return nil, kernelruntime.NewHostCallError("not_found", fmt.Sprintf("connector %q was not found", requested))
	}
	tools, err := s.mcpDirectory.ListUnifiedConnectorTools(ctx, userID, stringValue(items[0]["name"]))
	if err != nil {
		return nil, kernelHostRegistryError(err)
	}
	projected := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		projected = append(projected, map[string]any{"name": tool.Name, "description": tool.Description})
	}
	items[0]["tools"] = projected
	return items[0], nil
}

func compileOptionalKernelPattern(value, label string) (*regexp.Regexp, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	compiled, err := regexp.Compile(value)
	if err != nil {
		return nil, fmt.Errorf("%s is invalid: %w", label, err)
	}
	return compiled, nil
}

func kernelHostRegistryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return kernelruntime.NewHostCallError("not_found", err.Error())
	}
	if errors.Is(err, os.ErrExist) {
		return kernelruntime.NewHostCallError("conflict", err.Error())
	}
	if errors.Is(err, errCompatibilitySkillReadOnly) || strings.Contains(strings.ToLower(err.Error()), "permission") {
		return kernelruntime.NewHostCallError("permission_denied", err.Error())
	}
	return kernelruntime.NewHostCallError("storage_error", err.Error())
}

func kernelHostExpectNoArguments(args []any, kwargs map[string]any, method string) error {
	if len(args) != 0 || len(kwargs) != 0 {
		return fmt.Errorf("%s takes no arguments", method)
	}
	return nil
}

func kernelHostSingleNameInput(args []any, kwargs map[string]any, method string) (string, error) {
	value, err := kernelHostArgument(args, kwargs, 0, "name")
	if err != nil {
		return "", err
	}
	name, ok := value.(string)
	if !ok || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("%s requires a non-empty name", method)
	}
	if len(args) > 1 {
		return "", fmt.Errorf("%s received too many positional arguments", method)
	}
	return strings.TrimSpace(name), nil
}

func kernelHostArgument(args []any, kwargs map[string]any, index int, key string) (any, error) {
	if value, found := kwargs[key]; found {
		if len(args) > index {
			return nil, fmt.Errorf("%s was provided both positionally and by keyword", key)
		}
		return value, nil
	}
	if len(args) <= index {
		return nil, fmt.Errorf("%s is required", key)
	}
	return args[index], nil
}

func kernelHostOptionalStringInput(args []any, kwargs map[string]any, key string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("%s received too many positional arguments", key)
	}
	value, found := kwargs[key]
	if !found && len(args) == 0 {
		return "", nil
	}
	if !found {
		value = args[0]
	}
	if value == nil {
		return "", nil
	}
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(result), nil
}

func kernelHostSkillReadInput(args []any, kwargs map[string]any) (string, string, error) {
	name, err := kernelHostStringAt(args, kwargs, 0, "name")
	if err != nil {
		return "", "", err
	}
	if len(args) > 2 {
		return "", "", errors.New("host.skills.read received too many positional arguments")
	}
	path, err := kernelHostOptionalStringInput(args[1:], kwargs, "path")
	if err != nil {
		return "", "", err
	}
	if path == "" {
		path = "SKILL.md"
	}
	return name, filepath.ToSlash(path), nil
}

func kernelHostSkillEditInput(args []any, kwargs map[string]any) (string, string, string, string, error) {
	if len(args) > 4 {
		return "", "", "", "", errors.New("host.skills.edit received too many positional arguments")
	}
	name, err := kernelHostStringAt(args, kwargs, 0, "name")
	if err != nil {
		return "", "", "", "", err
	}
	path, err := kernelHostStringAt(args, kwargs, 1, "path")
	if err != nil {
		return "", "", "", "", err
	}
	content, err := kernelHostStringAt(args, kwargs, 2, "content")
	if err != nil {
		return "", "", "", "", err
	}
	oldString := ""
	if value, found := kwargs["old_string"]; found {
		var ok bool
		oldString, ok = value.(string)
		if !ok {
			return "", "", "", "", errors.New("old_string must be a string")
		}
	} else if len(args) > 3 {
		var ok bool
		oldString, ok = args[3].(string)
		if !ok {
			return "", "", "", "", errors.New("old_string must be a string")
		}
	}
	return strings.TrimSpace(name), filepath.ToSlash(strings.TrimSpace(path)), content, oldString, nil
}

func kernelHostSkillPublishInput(args []any, kwargs map[string]any) (string, bool, error) {
	name, err := kernelHostStringAt(args, kwargs, 0, "name")
	if err != nil {
		return "", false, err
	}
	if len(args) > 2 {
		return "", false, errors.New("host.skills.publish received too many positional arguments")
	}
	overwrite := false
	if value, found := kwargs["overwrite"]; found {
		parsed, ok := value.(bool)
		if !ok {
			return "", false, errors.New("overwrite must be a bool")
		}
		overwrite = parsed
	} else if len(args) > 1 {
		parsed, ok := args[1].(bool)
		if !ok {
			return "", false, errors.New("overwrite must be a bool")
		}
		overwrite = parsed
	}
	return name, overwrite, nil
}

func kernelHostSkillInstallInput(args []any, kwargs map[string]any) (string, []string, string, error) {
	if len(args) > 1 {
		return "", nil, "", errors.New("host.skills.install accepts one configuration object")
	}
	config := map[string]any{}
	if len(args) == 1 {
		value, ok := args[0].(map[string]any)
		if !ok {
			return "", nil, "", errors.New("Skill install configuration must be an object")
		}
		config = value
		if len(kwargs) != 0 {
			return "", nil, "", errors.New("Skill install configuration cannot mix positional and keyword fields")
		}
	} else {
		config = kwargs
	}
	for key := range config {
		if key != "repo" && key != "skills" && key != "sha" {
			return "", nil, "", fmt.Errorf("unknown Skill install field %q", key)
		}
	}
	repo := strings.TrimSpace(stringValue(config["repo"]))
	if repo == "" {
		return "", nil, "", errors.New("repo is required")
	}
	selected := []string{}
	if raw, found := config["skills"]; found && raw != nil {
		var err error
		selected, err = kernelHostStringSlice(raw, "skills")
		if err != nil {
			return "", nil, "", err
		}
	}
	return repo, selected, strings.TrimSpace(stringValue(config["sha"])), nil
}

func kernelHostStringAt(args []any, kwargs map[string]any, index int, key string) (string, error) {
	value, err := kernelHostArgument(args, kwargs, index, key)
	if err != nil {
		return "", err
	}
	result, ok := value.(string)
	if !ok || strings.TrimSpace(result) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return result, nil
}

func kernelHostAgentSkillInput(args []any, kwargs map[string]any, method string) (string, string, error) {
	name, err := kernelHostStringAt(args, kwargs, 0, "name")
	if err != nil {
		return "", "", err
	}
	skillName, err := kernelHostStringAt(args, kwargs, 1, "skill")
	if err != nil {
		return "", "", err
	}
	if len(args) > 2 {
		return "", "", fmt.Errorf("%s received too many positional arguments", method)
	}
	return strings.TrimSpace(name), strings.TrimSpace(skillName), nil
}

func kernelHostAgentConnectorInput(args []any, kwargs map[string]any, method string) (string, string, string, string, error) {
	name, err := kernelHostStringAt(args, kwargs, 0, "name")
	if err != nil {
		return "", "", "", "", err
	}
	connector, err := kernelHostStringAt(args, kwargs, 1, "connector")
	if err != nil {
		return "", "", "", "", err
	}
	if len(args) > 2 {
		return "", "", "", "", fmt.Errorf("%s received too many positional arguments", method)
	}
	include, err := kernelHostOptionalStringInput(nil, map[string]any{"include_tools_pattern": kwargs["include_tools_pattern"]}, "include_tools_pattern")
	if err != nil {
		return "", "", "", "", err
	}
	exclude, err := kernelHostOptionalStringInput(nil, map[string]any{"exclude_tools_pattern": kwargs["exclude_tools_pattern"]}, "exclude_tools_pattern")
	if err != nil {
		return "", "", "", "", err
	}
	return strings.TrimSpace(name), strings.TrimSpace(connector), include, exclude, nil
}

func kernelHostAgentCreateInput(args []any, kwargs map[string]any) (agentProfileCreateRequest, error) {
	if len(args) > 5 {
		return agentProfileCreateRequest{}, errors.New("host.agents.create received too many positional arguments")
	}
	read := func(index int, key string) (any, bool, error) {
		if value, found := kwargs[key]; found {
			if len(args) > index {
				return nil, false, fmt.Errorf("%s was provided twice", key)
			}
			return value, true, nil
		}
		if len(args) > index {
			return args[index], true, nil
		}
		return nil, false, nil
	}
	name, ok, err := read(0, "name")
	if err != nil || !ok {
		return agentProfileCreateRequest{}, firstKernelHostError(err, "name is required")
	}
	displayName, ok, err := read(1, "display_name")
	if err != nil || !ok {
		return agentProfileCreateRequest{}, firstKernelHostError(err, "display_name is required")
	}
	description, ok, err := read(2, "description")
	if err != nil || !ok {
		return agentProfileCreateRequest{}, firstKernelHostError(err, "description is required")
	}
	toText := func(value any, key string) (string, error) {
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("%s must be a string", key)
		}
		return text, nil
	}
	nameText, err := toText(name, "name")
	if err != nil {
		return agentProfileCreateRequest{}, err
	}
	displayText, err := toText(displayName, "display_name")
	if err != nil {
		return agentProfileCreateRequest{}, err
	}
	descriptionText, err := toText(description, "description")
	if err != nil {
		return agentProfileCreateRequest{}, err
	}
	input := agentProfileCreateRequest{Name: nameText, DisplayName: displayText, Description: descriptionText}
	if value, found, err := read(3, "system_prompt"); err != nil {
		return input, err
	} else if found {
		input.SystemPrompt, err = toText(value, "system_prompt")
		if err != nil {
			return input, err
		}
	}
	if value, found, err := read(4, "skill_names"); err != nil {
		return input, err
	} else if found {
		names, err := kernelHostStringSlice(value, "skill_names")
		if err != nil {
			return input, err
		}
		input.SkillNames = &names
	}
	if value, found := kwargs["unrestricted"]; found {
		unrestricted, ok := value.(bool)
		if !ok {
			return input, errors.New("unrestricted must be a bool")
		}
		input.Unrestricted = &unrestricted
	}
	if value, found := kwargs["enabled"]; found {
		enabled, ok := value.(bool)
		if !ok {
			return input, errors.New("enabled must be a bool")
		}
		input.Enabled = &enabled
	}
	return input, nil
}

func kernelHostAgentUpdateInput(args []any, kwargs map[string]any) (string, agentProfilePatchRequest, error) {
	name, err := kernelHostStringAt(args, kwargs, 0, "name")
	if err != nil {
		return "", agentProfilePatchRequest{}, err
	}
	if len(args) > 2 {
		return "", agentProfilePatchRequest{}, errors.New("host.agents.update received too many positional arguments")
	}
	var raw map[string]any
	if len(args) > 1 {
		var ok bool
		raw, ok = args[1].(map[string]any)
		if !ok {
			return "", agentProfilePatchRequest{}, errors.New("agent update patch must be an object")
		}
	} else if value, found := kwargs["patch"]; found {
		var ok bool
		raw, ok = value.(map[string]any)
		if !ok {
			return "", agentProfilePatchRequest{}, errors.New("agent update patch must be an object")
		}
	} else {
		raw = kwargs
	}
	patch := agentProfilePatchRequest{}
	stringField := func(keys ...string) (*string, error) {
		for _, key := range keys {
			if value, found := raw[key]; found {
				text, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("%s must be a string", key)
				}
				return &text, nil
			}
		}
		return nil, nil
	}
	var fieldErr error
	patch.DisplayName, fieldErr = stringField("display_name", "displayName")
	if fieldErr != nil {
		return "", patch, fieldErr
	}
	patch.Description, fieldErr = stringField("description")
	if fieldErr != nil {
		return "", patch, fieldErr
	}
	patch.SystemPrompt, fieldErr = stringField("system_prompt", "systemPrompt")
	if fieldErr != nil {
		return "", patch, fieldErr
	}
	patch.Name, fieldErr = stringField("name")
	if fieldErr != nil {
		return "", patch, fieldErr
	}
	if value, found := raw["skill_names"]; found {
		names, err := kernelHostStringSlice(value, "skill_names")
		if err != nil {
			return "", patch, err
		}
		patch.SkillNames = &names
	}
	if value, found := raw["unrestricted"]; found {
		unrestricted, ok := value.(bool)
		if !ok {
			return "", patch, errors.New("unrestricted must be a bool")
		}
		patch.Unrestricted = &unrestricted
	}
	return strings.TrimSpace(name), patch, nil
}

func kernelHostStringSlice(value any, key string) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return append([]string(nil), strings...), nil
		}
		return nil, fmt.Errorf("%s must be a list of strings", key)
	}
	result := make([]string, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", key, index)
		}
		result[index] = text
	}
	return result, nil
}

func firstKernelHostError(err error, fallback string) error {
	if err != nil {
		return err
	}
	return errors.New(fallback)
}
