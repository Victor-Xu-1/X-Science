package server

import "synon-go/internal/sciencecapability"

// Recovery describes the same registered route that the execution boundary
// validates. It neither invents an alternative nor authorizes its values.
func executionDocumentedRecoveryOption(pack sciencecapability.ExecutionPack, group string) map[string]any {
	route, found := pack.DocumentedInput(group)
	if !found || group == "" {
		return nil
	}
	parameters := []map[string]string{}
	replaces := []string{}
	for _, parameter := range pack.Parameters {
		if parameter.EvidenceGroup == group && parameter.Evidence == "resolved-user-input" {
			parameters = append(parameters, map[string]string{
				"name": parameter.Name, "argument": parameter.Argument, "type": parameter.Type,
			})
		}
		if parameter.InputEvidence != nil && parameter.InputEvidence.EvidenceGroup == group {
			replaces = append(replaces, parameter.Argument)
		}
	}
	inputArgument := ""
	for _, input := range pack.Inputs {
		if input.Kind == route.InputKind {
			inputArgument = input.Argument
			break
		}
	}
	return map[string]any{
		"schema": "synon.documented-input.v1", "argument": route.Argument,
		"evidence_group": route.EvidenceGroup, "input_kind": route.InputKind, "input_argument": inputArgument,
		"parameters": parameters, "replaces_arguments": replaces,
		"record_fields": []string{"schema", "evidence_group", "input_sha256", "basis", "method", "sources", "limitations", "values"},
	}
}
