package server

import (
	"slices"
	"sort"
	"strings"

	"x-science/internal/agentruntime"
	"x-science/internal/skills"
)

// Summarize discovery without copying the complete deferred method catalog
// into every request. The same admitted schemas remain execution authority;
// loaded Skills retain exact method identities and the live host catalog owns
// server identity, parameters and method discovery.
func runtimeMCPContext(schemas []agentruntime.ToolSchema, selectedSkills []skills.Skill) string {
	connectors := map[string]struct{}{}
	selectedMethods := map[string]struct{}{}
	selectedByTool := selectedSkillMCPToolMap(selectedSkills)
	for _, schema := range schemas {
		name := strings.TrimSpace(schema.Name)
		if !strings.HasPrefix(strings.ToLower(name), "mcp__") {
			continue
		}
		connector, method, valid := strings.Cut(name[len("mcp__"):], "__")
		if !valid || connector == "" || method == "" {
			continue
		}
		connectors[connector] = struct{}{}
		if len(selectedByTool[name]) > 0 {
			selectedMethods[name] = struct{}{}
		}
	}
	if len(connectors) == 0 {
		return ""
	}
	lines := []string{
		"Invocation contract:",
		"- Use only an exact tool name from the admitted live catalog; never invent or rename a connector namespace.",
		"- Preserve JSON Schema types exactly: numbers are unquoted JSON numbers and arrays are JSON arrays, not encoded strings.",
		"- When a call returns invalid_tool_arguments, repair only the reported paths before one corrected call; do not repeat the same invalid arguments.",
		"- Discover exact methods and parameters on demand: load a matching MCP skill with filter, or use host.mcp.list_servers() and host.mcp.list_methods(server) inside repl. Use the returned server identity, not a guessed namespace hint.",
		"Connector namespace hints:",
	}
	connectorNames := make([]string, 0, len(connectors))
	for connector := range connectors {
		connectorNames = append(connectorNames, connector)
	}
	sort.Strings(connectorNames)
	for _, connector := range connectorNames {
		lines = append(lines, "- "+connector)
	}
	if len(selectedMethods) > 0 {
		lines = append(lines, "Methods referenced by loaded Skills:")
		methodNames := make([]string, 0, len(selectedMethods))
		for name := range selectedMethods {
			methodNames = append(methodNames, name)
		}
		sort.Strings(methodNames)
		for _, name := range methodNames {
			lines = append(lines, "- "+name+" | selectedBySkill="+strings.Join(selectedByTool[name], ","))
		}
	}
	return strings.Join(lines, "\n")
}

func selectedSkillMCPToolMap(selectedSkills []skills.Skill) map[string][]string {
	selectedByTool := map[string][]string{}
	for _, skill := range selectedSkills {
		skillName := strings.TrimSpace(skill.Name)
		if skillName == "" {
			continue
		}
		for _, toolName := range skill.Tools {
			toolName = strings.TrimSpace(toolName)
			if !strings.HasPrefix(toolName, "mcp__") {
				continue
			}
			if !slices.Contains(selectedByTool[toolName], skillName) {
				selectedByTool[toolName] = append(selectedByTool[toolName], skillName)
			}
		}
	}
	for toolName := range selectedByTool {
		sort.Strings(selectedByTool[toolName])
	}
	return selectedByTool
}
