package server

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"x-science/internal/agentruntime"
)

type sessionRunnerExplicitToolRequirement struct {
	Name       string
	MinResults int
}

// sessionRunnerExplicitToolContract contains heuristic observations from task
// prose, not typed execution intent. Mentions can be explanatory or negative;
// comparing them with receipts can disclose uncertainty but cannot require a
// call, authorize execution, reject delivery, or certify task fulfilment.
type sessionRunnerExplicitToolContract struct {
	Requirements           []sessionRunnerExplicitToolRequirement
	PostWriteReads         []string
	ReportFirstFailureCode bool
	RequireMCP             bool
}

var sessionRunnerExplicitSkillTokenPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_])skills?(?:$|[^a-z0-9_])`)

func buildSessionRunnerExplicitToolContract(taskIntent string) sessionRunnerExplicitToolContract {
	normalized := strings.ToLower(strings.TrimSpace(taskIntent))
	compact := strings.Join(strings.Fields(normalized), "")
	if normalized == "" {
		return sessionRunnerExplicitToolContract{}
	}
	contract := sessionRunnerExplicitToolContract{}
	add := func(name string, minimum int) {
		for index := range contract.Requirements {
			if contract.Requirements[index].Name != name {
				continue
			}
			if minimum > contract.Requirements[index].MinResults {
				contract.Requirements[index].MinResults = minimum
			}
			return
		}
		contract.Requirements = append(contract.Requirements, sessionRunnerExplicitToolRequirement{Name: name, MinResults: minimum})
	}
	if strings.Contains(normalized, "search_skills") {
		add("search_skills", 1)
	}
	if strings.Contains(normalized, "web_search") {
		add("web_search", 1)
	}
	if strings.Contains(normalized, "manage_packages") {
		add("manage_packages", 1)
	}
	if sessionRunnerExplicitSkillTokenPattern.MatchString(normalized) && containsAny(normalized, []string{
		"使用", "调用", "加载", "use", "using", "invoke", "load",
	}) {
		minimum := 1
		if strings.Contains(compact, "至少两个") || strings.Contains(normalized, "at least two") {
			minimum = 2
		}
		add("skill", minimum)
	}
	contract.RequireMCP = strings.Contains(normalized, "mcp") &&
		(strings.Contains(normalized, "真实") || strings.Contains(normalized, "actual") || strings.Contains(normalized, "method") || strings.Contains(normalized, "方法"))
	contract.PostWriteReads = sessionRunnerExplicitPostWriteReadTargets(taskIntent)
	contract.ReportFirstFailureCode = sessionRunnerRequiresFirstFailureCodeReport(taskIntent)
	return contract
}

func (contract sessionRunnerExplicitToolContract) gaps(messages []agentruntime.Message) []string {
	completed := sessionRunnerSuccessfulToolExecutionIndex(messages)
	gaps := make([]string, 0, len(contract.Requirements))
	for _, requirement := range contract.Requirements {
		actual := completed[requirement.Name]
		if actual >= requirement.MinResults {
			continue
		}
		if requirement.MinResults == 1 {
			gaps = append(gaps, fmt.Sprintf("task text mentions %s, but no successful tool result was observed", requirement.Name))
			continue
		}
		gaps = append(gaps, fmt.Sprintf(
			"task text suggests %d %s results, but the durable successful tool index contains %d",
			requirement.MinResults, requirement.Name, actual,
		))
	}
	if contract.RequireMCP {
		hasMCP := false
		for name, count := range completed {
			if count > 0 && strings.HasPrefix(name, "mcp__") {
				hasMCP = true
				break
			}
		}
		if !hasMCP {
			gaps = append(gaps, "task text mentions live MCP execution, but no successful MCP tool result was observed")
		}
	}
	gaps = append(gaps, sessionRunnerExplicitPostWriteReadGaps(contract.PostWriteReads, messages)...)
	// Data-domain coverage is semantic, not structural. A single connector can
	// expose several independent sources (for example chemistry aggregators),
	// while two methods on two connectors can still represent the same source.
	// The evidence reviewer sees the attested method names, inputs, and results
	// and owns that judgment. Do not reinterpret examples in free-form user text
	// as hard requirements or substitute connector count for domain count here.
	return gaps
}

func sessionRunnerSuccessfulToolExecutionIndex(messages []agentruntime.Message) map[string]int {
	callNames := make(map[string]string)
	succeeded := make(map[string]bool)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			callID := strings.TrimSpace(call.ID)
			name := strings.ToLower(strings.TrimSpace(call.Name))
			if callID != "" && name != "" {
				callNames[callID] = name
			}
		}
		if message.Role != "tool" || strings.TrimSpace(message.ToolCallID) == "" {
			continue
		}
		var result any
		if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
			result = message.Content
		}
		if agentruntime.ClassifyToolResult(result) == agentruntime.ToolResultSucceeded {
			succeeded[strings.TrimSpace(message.ToolCallID)] = true
		}
	}
	index := make(map[string]int)
	for callID, name := range callNames {
		if succeeded[callID] {
			index[name]++
		}
	}
	return index
}
