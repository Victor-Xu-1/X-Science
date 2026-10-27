package server

import (
	"strings"

	"x-science/internal/skills"
)

// Both live loading and authenticated replay render the captured, permitted
// closure through this owner. It never reads the catalog or persists a load.
func renderRuntimeSkillInvocation(items []skills.Skill, root, arguments, filter string) ([]skills.Skill, string, error) {
	effective := append([]skills.Skill(nil), items...)
	filter = strings.TrimSpace(filter)
	for index := range effective {
		item := &effective[index]
		if !strings.EqualFold(strings.TrimSpace(item.Name), strings.TrimSpace(root)) ||
			filter == "" || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.Name)), "mcp-") {
			continue
		}
		if body, found := runtimeMCPConnectorFilterRenderedBody(item.Body, filter); found {
			item.Body, item.BodyHash = body, serverStringHash(body)
		}
	}
	prompt, err := renderComposedSkillPrompt(effective, root, arguments)
	return effective, prompt, err
}

// Keep the original metadata/identity separate from the one rendered context
// copy. Dependency arguments stay empty, exactly as in composed live loading.
func runtimeSkillInvocationContext(items []skills.Skill, root, arguments string) []skills.Skill {
	context := append([]skills.Skill(nil), items...)
	for index := range context {
		itemArguments := ""
		if strings.EqualFold(strings.TrimSpace(context[index].Name), strings.TrimSpace(root)) {
			itemArguments = arguments
		}
		context[index].Body = renderSkillPrompt(context[index], itemArguments)
		context[index].BodyHash = serverStringHash(context[index].Body)
	}
	return context
}

// Decode the live producer's full readable envelope, not its identity-only
// legacy replay or a descriptor preview. Header hashes remain diagnostic; the
// caller separately proves the host source and compares this whole body with
// the current permitted closure rendered using the original executed input.
func runtimeSkillReplayRenderedPrompt(value any, root string) (string, bool) {
	if payload, ok := value.(map[string]any); ok {
		prompt, full := payload["prompt"].(string)
		return prompt, full && strings.TrimSpace(prompt) != ""
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	if strings.HasPrefix(strings.TrimSpace(text), agentRuntimeLegacySkillMetadataPrefix) {
		return "", false
	}
	if !strings.HasPrefix(text, agentRuntimeSkillMetadataPrefix) {
		return text, true
	}
	name, valid := providerSkillResultName(text)
	boundary := strings.Index(text, "\n\n")
	suffix := "\n\n" + agentRuntimeSkillActivePostlude
	if !valid || normalizedRuntimeSkillReceiptName(name) != normalizedRuntimeSkillReceiptName(root) ||
		boundary < 0 || !strings.HasSuffix(text, suffix) {
		return "", false
	}
	body := strings.TrimSuffix(text[boundary+2:], suffix)
	return body, strings.TrimSpace(body) != ""
}
