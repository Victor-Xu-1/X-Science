package server

import (
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

// The authoring return already has validated name/description fields. Its
// Markdown is a procedure, not a second metadata authority. Serialize the one
// canonical header here; never parse model-authored YAML to obtain catalog,
// capability or tool declarations. External Skill imports retain their own
// unchanged loader and validation contracts.
func normalizeConversationSkillManifest(raw, name, description string) (string, error) {
	body := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if strings.HasPrefix(body, "---\n") {
		lines := strings.Split(body, "\n")
		for index := 1; index < len(lines); index++ {
			if strings.TrimRight(lines[index], " \t") == "---" {
				// A complete leading header is redundant model output, even if
				// not valid YAML. Only delimiter lines, never a prefix or an
				// arbitrary body span, define this boundary. Without a closing
				// delimiter the text remains ordinary Markdown (a thematic break).
				if authoredSkillHeaderHasMetadata(lines[1:index]) {
					body = strings.TrimSpace(strings.Join(lines[index+1:], "\n"))
				}
				break
			}
		}
	}
	if body == "" {
		return "", errors.New("skill_markdown must contain a procedure")
	}
	// Quoted scalars keep delimiter-looking description lines inside their
	// value; a multiline literal must not become a second header/body boundary.
	metadata := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: name, Style: yaml.DoubleQuotedStyle},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "description"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: description, Style: yaml.DoubleQuotedStyle},
	}}
	encoded, err := yaml.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return "---\n" + strings.TrimSpace(string(encoded)) + "\n---\n\n" + body + "\n", nil
}

// Recognize only a complete leading wrapper carrying the declared metadata
// keys. A Markdown introduction between thematic breaks is method content,
// not a header to discard. Values are deliberately not interpreted as YAML.
func authoredSkillHeaderHasMetadata(lines []string) bool {
	for _, line := range lines {
		key, _, found := strings.Cut(strings.TrimSpace(line), ":")
		if found {
			key = strings.ToLower(strings.Trim(strings.TrimSpace(key), "\"'"))
			if key == "name" || key == "description" {
				return true
			}
		}
	}
	return false
}
