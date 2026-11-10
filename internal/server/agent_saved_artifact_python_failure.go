package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

var errAgentSavedPythonValidatorUnavailable = errors.New("saved Python artifact validator is unavailable")

// Private host classification: the original cause remains available to Is/As,
// while Error and model feedback never include source, stderr or executable paths.
type agentSavedPythonValidationError struct {
	contentInvalid  bool
	validationCode  string
	unresolvedCount int
	cause           error
}

func (e *agentSavedPythonValidationError) Error() string {
	return "saved Python artifact validation: " + e.validationCode
}

func (e *agentSavedPythonValidationError) Unwrap() error { return e.cause }

func agentSavedPythonValidatorFailure(code string, cause error) error {
	switch code {
	case "runtime_unavailable", "process_failed", "diagnostic_limit_exceeded", "invalid_result":
	default:
		code = "invalid_result"
	}
	if code == "process_failed" && (errors.Is(cause, os.ErrNotExist) || errors.Is(cause, exec.ErrNotFound)) {
		code = "interpreter_missing"
	}
	return &agentSavedPythonValidationError{
		validationCode: code, cause: errors.Join(errAgentSavedPythonValidatorUnavailable, cause),
	}
}

func classifyAgentSavedPythonResult(result agentSavedPythonStaticValidation) (agentSavedPythonAnalysis, error) {
	consistent := result.UnresolvedCount >= len(result.Unresolved) && len(result.Unresolved) <= 32 &&
		result.IndeterminateCount >= len(result.Indeterminate) && len(result.Indeterminate) <= 32
	valid, indeterminate, invalid := false, false, false
	if consistent {
		switch result.Code {
		case "valid_python":
			valid = result.OK && result.UnresolvedCount == 0 && result.IndeterminateCount == 0
		case "analysis_indeterminate":
			indeterminate = !result.OK && result.UnresolvedCount == 0 && result.IndeterminateCount > 0
		case "syntax_error":
			invalid = !result.OK && result.UnresolvedCount == 0 && result.IndeterminateCount == 0
		case "unresolved_global":
			invalid = !result.OK && result.UnresolvedCount > 0
		}
	}
	if valid {
		return agentSavedPythonValid, nil
	}
	if indeterminate {
		return agentSavedPythonIndeterminate, nil
	}
	if !invalid {
		return agentSavedPythonValid, agentSavedPythonValidatorFailure("invalid_result", nil)
	}
	return agentSavedPythonValid, &agentSavedPythonValidationError{
		contentInvalid: true, validationCode: result.Code,
		unresolvedCount: result.UnresolvedCount, cause: errAgentSavedArtifactPythonInvalid,
	}
}

func agentSavedPythonFailureFeedback(path string, err error) (map[string]any, bool) {
	var classified *agentSavedPythonValidationError
	typed := errors.As(err, &classified)
	if !typed && !errors.Is(err, errAgentSavedArtifactPythonInvalid) {
		return nil, false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return agentSaveArtifactFailurePayload(path, "cancelled", nil, 0), true
	}
	contentInvalid := errors.Is(err, errAgentSavedArtifactPythonInvalid)
	if typed {
		contentInvalid = classified.contentInvalid
	}
	code, validationCode := "python_validator_unavailable", "invalid_result"
	if contentInvalid {
		code, validationCode = "invalid_python_artifact", "invalid_python"
	}
	if typed {
		switch classified.validationCode {
		case "syntax_error", "unresolved_global", "runtime_unavailable", "process_failed",
			"interpreter_missing", "diagnostic_limit_exceeded", "invalid_result":
			validationCode = classified.validationCode
		}
	}
	failure := agentSaveArtifactFailurePayload(path, code, nil, 0)
	failure["validation_stage"] = "python_validation"
	failure["validation_code"] = validationCode
	recovery := mapValue(failure["recovery"])
	if contentInvalid {
		recovery["action"] = "repair_python_source_then_retry_only_that_file"
		recovery["diagnostic"] = "inspect the Python source using validation_code; validator details and source text are not echoed"
		// Omit unusually large counts rather than changing validation or
		// returning a rounded/saturated number as if it were exact.
		if typed && classified.unresolvedCount > 0 && classified.unresolvedCount <= 1_000_000 {
			failure["validation_unresolved_count"] = classified.unresolvedCount
		}
	} else {
		recovery["action"] = "restore_the_python_validator_then_retry_only_that_file"
		recovery["data_policy"] = "validator failure is not evidence of invalid file content; preserve the file"
	}
	return failure, true
}
