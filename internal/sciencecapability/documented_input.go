package sciencecapability

import "errors"

// ExecutionDocumentedInput is an alternative to a resolver-produced input,
// not a way to claim resolver success. It records the task's method, sources,
// limitations, exact parameter values and primary input digest.
type ExecutionDocumentedInput struct {
	EvidenceGroup string `json:"evidenceGroup"`
	Argument      string `json:"argument"`
	InputKind     string `json:"inputKind"`
}

func (pack ExecutionPack) DocumentedInput(group string) (ExecutionDocumentedInput, bool) {
	for _, route := range pack.DocumentedInputs {
		if route.EvidenceGroup == group {
			return route, true
		}
	}
	return ExecutionDocumentedInput{}, false
}

func validateDocumentedInputs(pack ExecutionPack) error {
	if len(pack.DocumentedInputs) > 64 {
		return errors.New("too many documented input routes")
	}
	groups, arguments := map[string]bool{}, map[string]bool{}
	for _, route := range pack.DocumentedInputs {
		if pack.Mode != "local" || !identifierPattern.MatchString(route.EvidenceGroup) || groups[route.EvidenceGroup] || arguments[route.Argument] {
			return errors.New("invalid or duplicate documented input route")
		}
		fileFound, valuesFound, inputFound := false, false, false
		for _, parameter := range pack.Parameters {
			if parameter.Argument == route.Argument {
				fileFound = parameter.Type == "string" && parameter.Evidence == "" && parameter.InputEvidence == nil && parameter.Default == nil
			}
			if parameter.EvidenceGroup == route.EvidenceGroup && parameter.Evidence == "resolved-user-input" {
				valuesFound = true
			}
		}
		for _, input := range pack.Inputs {
			inputFound = inputFound || input.Kind == route.InputKind
			if input.Argument == route.Argument {
				return errors.New("documented input overlaps primary input")
			}
		}
		if !fileFound || !valuesFound || !inputFound {
			return errors.New("documented input route has an unresolved binding")
		}
		groups[route.EvidenceGroup], arguments[route.Argument] = true, true
	}
	return nil
}
