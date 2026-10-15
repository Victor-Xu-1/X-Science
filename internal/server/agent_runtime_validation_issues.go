package server

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

const maxAgentRuntimeValidationPointerBytes = 512

// Project typed validator failures, never their localized messages or input
// values. Admission still uses the complete, unchanged compiled schema.
func agentRuntimeMCPValidationIssues(schema map[string]any, input map[string]any, err error) []map[string]any {
	root, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []map[string]any{{"path": "", "keyword": "schema", "actual_type": agentRuntimeJSONType(input)}}
	}
	leaves := make([]*jsonschema.ValidationError, 0, maxAgentRuntimeMCPValidationIssues)
	var visit func(*jsonschema.ValidationError)
	visit = func(current *jsonschema.ValidationError) {
		if current == nil || len(leaves) >= maxAgentRuntimeMCPValidationIssues {
			return
		}
		if len(current.Causes) == 0 {
			leaves = append(leaves, current)
			return
		}
		for _, cause := range current.Causes {
			visit(cause)
		}
	}
	visit(root)
	issues := make([]map[string]any, 0, maxAgentRuntimeMCPValidationIssues)
	seen := map[string]struct{}{}
	appendIssue := func(parts []string, keyword, actualType string, expected []string) {
		if len(issues) >= maxAgentRuntimeMCPValidationIssues {
			return
		}
		path, complete := agentRuntimeValidationPointer(parts)
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%t", path, keyword, strings.Join(expected, ","), actualType, complete)
		if _, found := seen[key]; found {
			return
		}
		seen[key] = struct{}{}
		issue := map[string]any{"path": path, "keyword": keyword, "actual_type": actualType}
		if !complete {
			issue["path_omitted"] = true
		}
		if len(expected) > 0 {
			issue["expected"] = expected
		}
		issues = append(issues, issue)
	}
	for _, leaf := range leaves {
		if len(issues) >= maxAgentRuntimeMCPValidationIssues {
			break
		}
		switch failure := leaf.ErrorKind.(type) {
		case *kind.Required:
			for _, missing := range failure.Missing {
				parts := append(append([]string(nil), leaf.InstanceLocation...), missing)
				appendIssue(parts, "required", "missing", agentRuntimeSchemaJSONTypes(agentRuntimeSchemaAt(schema, parts)))
			}
		case *kind.AdditionalProperties:
			// Preserve the existing same-object missing-field repair priority.
			// Compare original segments before expansion or length omission.
			if agentRuntimeHasRequiredFailureAt(leaves, leaf.InstanceLocation) {
				continue
			}
			names := append([]string(nil), failure.Properties...)
			sort.Strings(names)
			for _, name := range names {
				if len(issues) >= maxAgentRuntimeMCPValidationIssues {
					break
				}
				parts := append(append([]string(nil), leaf.InstanceLocation...), name)
				appendIssue(parts, "additionalProperties", agentRuntimeJSONType(agentRuntimeValueAt(input, parts)), nil)
			}
		default:
			keyword := "schema"
			if leaf.ErrorKind != nil {
				if path := leaf.ErrorKind.KeywordPath(); len(path) > 0 {
					keyword = path[len(path)-1]
				}
			}
			appendIssue(leaf.InstanceLocation, keyword, agentRuntimeJSONType(agentRuntimeValueAt(input, leaf.InstanceLocation)),
				agentRuntimeSchemaJSONTypes(agentRuntimeSchemaAt(schema, leaf.InstanceLocation)))
		}
	}
	if len(issues) == 0 {
		appendIssue(nil, "schema", agentRuntimeJSONType(input), nil)
	}
	sort.SliceStable(issues, func(i, j int) bool {
		left, right := fmt.Sprint(issues[i]["path"]), fmt.Sprint(issues[j]["path"])
		if left != right {
			return left < right
		}
		return fmt.Sprint(issues[i]["keyword"]) < fmt.Sprint(issues[j]["keyword"])
	})
	return issues
}

func agentRuntimeHasRequiredFailureAt(leaves []*jsonschema.ValidationError, location []string) bool {
	for _, leaf := range leaves {
		if _, required := leaf.ErrorKind.(*kind.Required); required && slices.Equal(location, leaf.InstanceLocation) {
			return true
		}
	}
	return false
}

// Do not truncate a pointer into a different valid property name. The caller
// explicitly marks an omitted location while retaining the safe failure kind.
func agentRuntimeValidationPointer(parts []string) (string, bool) {
	var pointer strings.Builder
	for _, part := range parts {
		size := 1 + len(part) + strings.Count(part, "~") + strings.Count(part, "/")
		if size > maxAgentRuntimeValidationPointerBytes-pointer.Len() {
			return "", false
		}
		pointer.WriteByte('/')
		pointer.WriteString(strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1"))
	}
	return pointer.String(), true
}
