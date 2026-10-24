package server

import (
	"regexp"
	"strconv"
	"strings"
)

// Match only the original template. Argument data is inserted literally once;
// dollar tokens inside it must not become a second template or regex expansion.
func substituteSkillArguments(content string, args string, argumentNames []string) string {
	parsed := parseSkillArguments(args)
	argumentAt := func(index int) string {
		if index >= 0 && index < len(parsed) {
			return parsed[index]
		}
		return ""
	}
	var names []string
	positions := make(map[string]int)
	for index, name := range argumentNames {
		if strings.TrimSpace(name) == "" || isServerNumericString(name) {
			continue
		}
		if _, duplicate := positions[name]; !duplicate {
			names = append(names, regexp.QuoteMeta(name))
			positions[name] = index
		}
	}
	var patterns []string
	if len(names) > 0 {
		patterns = append(patterns, `(\$(?:`+strings.Join(names, "|")+`)\b)`)
	}
	// Alternatives retain declared-name, indexed, shorthand and raw precedence.
	// At most four captures are retained, independent of declared-name count.
	indexedRule := len(patterns)
	patterns = append(patterns, `(\$ARGUMENTS\[\d+\])`, `(\$\d+\b)`, `(\$ARGUMENTS)`)
	pattern := regexp.MustCompile(strings.Join(patterns, "|"))
	var rendered strings.Builder
	end := 0
	for end < len(content) {
		match := pattern.FindStringSubmatchIndex(content[end:])
		if match == nil {
			break
		}
		start, finish := end+match[0], end+match[1]
		rendered.WriteString(content[end:start])
		for rule := 0; rule < len(patterns); rule++ {
			if match[2+2*rule] < 0 {
				continue
			}
			value := ""
			switch rule {
			case indexedRule:
				index, err := strconv.Atoi(content[start+len("$ARGUMENTS[") : finish-1])
				if err == nil {
					value = argumentAt(index)
				}
			case indexedRule + 1:
				index, err := strconv.Atoi(content[start+1 : finish])
				if err == nil {
					value = argumentAt(index)
				}
			case indexedRule + 2:
				value = args
			default:
				value = argumentAt(positions[content[start+1:finish]])
			}
			rendered.WriteString(value)
			break
		}
		end = finish
	}
	rendered.WriteString(content[end:])
	result := rendered.String()
	if result == content && strings.TrimSpace(args) != "" {
		result += "\n\nARGUMENTS: " + args
	}
	return result
}
