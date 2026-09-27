package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const maxAgentRuntimeArgumentDiagnosticBytes = 1800

func copyArgumentFeedbackObject(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

// Admission always uses the complete schema. Feedback may focus an alternative
// only when supplied const/enum discriminators prove a unique branch; missing
// or ambiguous selectors keep the original validation evidence.
func agentRuntimeArgumentDiagnosticSchema(schema, input map[string]any, original error) (map[string]any, error) {
	view := copyArgumentFeedbackObject(schema)
	changed := false
	for _, keyword := range []string{"oneOf", "anyOf"} {
		var selected map[string]any
		candidates := 0
		observed := false
		for _, raw := range anySliceValue(schema[keyword]) {
			branch, ok := raw.(map[string]any)
			if !ok {
				candidates++
				continue
			}
			possible, matched := agentRuntimeArgumentBranchMatches(branch, input)
			if possible {
				candidates++
				selected, observed = branch, matched
			}
		}
		if candidates != 1 || selected == nil || !observed {
			continue
		}
		delete(view, keyword)
		constraints := append([]any(nil), anySliceValue(view["allOf"])...)
		view["allOf"] = append(constraints, selected)
		changed = true
	}
	if !changed {
		return schema, original
	}
	compiled, err := compileKernelDraft7Schema(kernelMCPOracleInputSchema(view))
	if err != nil {
		return schema, original
	}
	validator := &kernelMCPInputValidator{schema: compiled}
	if err := validator.Validate(input); err != nil {
		return view, err
	}
	// A projection can never turn a rejected call into a successful admission.
	return schema, original
}

func agentRuntimeArgumentBranchMatches(branch, input map[string]any) (bool, bool) {
	observed := false
	for name, raw := range mapValue(branch["properties"]) {
		actual, present := input[name]
		if !present {
			continue
		}
		property := mapValue(raw)
		if expected, constrained := property["const"]; constrained {
			observed = true
			if !agentRuntimeSchemaValueEqual(actual, expected) {
				return false, true
			}
		}
		if choices := anySliceValue(property["enum"]); len(choices) > 0 {
			observed = true
			matched := false
			for _, choice := range choices {
				matched = matched || agentRuntimeSchemaValueEqual(actual, choice)
			}
			if !matched {
				return false, true
			}
		}
	}
	return true, observed
}

func agentRuntimeSchemaValueEqual(left, right any) bool {
	a, leftErr := json.Marshal(left)
	b, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(a, b)
}

func agentRuntimeCompactArgumentContract(schema map[string]any) map[string]any {
	properties := map[string]any{}
	required := map[string]bool{}
	var collect func(map[string]any, int)
	collect = func(part map[string]any, depth int) {
		if depth > 16 {
			return
		}
		for name, value := range mapValue(part["properties"]) {
			property := copyArgumentFeedbackObject(mapValue(properties[name]))
			for key, constraint := range mapValue(value) {
				property[key] = constraint
			}
			properties[name] = property
		}
		for _, raw := range anySliceValue(part["required"]) {
			if name := strings.TrimSpace(fmt.Sprint(raw)); name != "" {
				required[name] = true
			}
		}
		for _, raw := range anySliceValue(part["allOf"]) {
			collect(mapValue(raw), depth+1)
		}
	}
	collect(schema, 0)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if required[names[i]] != required[names[j]] {
			return required[names[i]]
		}
		return names[i] < names[j]
	})
	total := len(names)
	if len(names) > 24 {
		names = names[:24]
	}
	fields := make(map[string]any, len(names))
	for _, name := range names {
		property := mapValue(properties[name])
		field := map[string]any{"types": agentRuntimeSchemaJSONTypes(property), "required": required[name]}
		if values := anySliceValue(property["enum"]); len(values) > 0 && len(values) <= 12 {
			field["enum"] = values
		}
		if value, present := property["const"]; present {
			field["const"] = value
		}
		fields[name] = field
	}
	contract := map[string]any{"type": "object", "fields": fields, "additionalProperties": schema["additionalProperties"]}
	if total > len(fields) {
		contract["omitted_fields"] = total - len(fields)
	}
	return contract
}

// Budget whole JSON members, never serialized bytes. Keep actionable issues
// ahead of optional schema fields and disclose omissions without changing the
// admitted schema or echoing argument values.
func boundedAgentRuntimeArgumentDiagnostic(diagnostic map[string]any) string {
	encode := func(value map[string]any) (string, bool) {
		raw, err := json.Marshal(value)
		return string(raw), err == nil && len(raw) <= maxAgentRuntimeArgumentDiagnosticBytes
	}
	if raw, fits := encode(diagnostic); fits {
		return raw
	}
	value := copyArgumentFeedbackObject(diagnostic)
	value["truncated"] = true
	contract := copyArgumentFeedbackObject(mapValue(value["expectedArguments"]))
	fields := copyArgumentFeedbackObject(mapValue(contract["fields"]))
	contract["fields"] = fields
	value["expectedArguments"] = contract
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		left, right := mapValue(fields[names[i]])["required"] == true, mapValue(fields[names[j]])["required"] == true
		if left != right {
			return !left
		}
		return names[i] > names[j]
	})
	omitted := int(numberValue(contract["omitted_fields"]))
	for _, name := range names {
		delete(fields, name)
		omitted++
		contract["omitted_fields"] = omitted
		if raw, fits := encode(value); fits {
			return raw
		}
	}
	delete(value, "expectedArguments")
	issues := sanitizedAgentRuntimeValidationIssues(value["issues"])
	for remaining := len(issues); remaining >= 0; remaining-- {
		value["issues"] = issues[:remaining]
		value["omitted_issues"] = len(issues) - remaining
		if raw, fits := encode(value); fits {
			return raw
		}
	}
	return `{"code":"invalid_tool_arguments","message":"Inspect the current admitted tool schema; repair feedback exceeded its display budget.","truncated":true}`
}
