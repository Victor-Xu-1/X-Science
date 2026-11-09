package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestAgentSavedPythonFailureClassifiesContentAndUnavailable(t *testing.T) {
	const privateDetail = "private source https://example.invalid/secret"
	var unavailable *Server
	// The nil-manager owner returns before seeking or reading this placeholder.
	// No interpreter, file IO or source execution is needed for this regression.
	missingRuntime := unavailable.validateAgentSavedPythonArtifact(context.Background(), "synthetic.py", &os.File{})
	for _, test := range []struct {
		name, code, action string
		err                error
	}{
		{"content", "invalid_python_artifact", "repair_python_source_then_retry_only_that_file",
			fmt.Errorf("%w: %s", errAgentSavedArtifactPythonInvalid, privateDetail)},
		{"unavailable", "python_validator_unavailable", "restore_the_python_validator_then_retry_only_that_file", missingRuntime},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.err == nil {
				t.Fatal("fixture did not produce the original validation error")
			}
			failure := agentSaveArtifactFailure("synthetic.py", test.err)
			if failure["code"] != test.code || mapValue(failure["recovery"])["action"] != test.action {
				t.Fatalf("Python save failure lost its cause class: %#v", failure)
			}
			encoded, err := json.Marshal(failure)
			if err != nil || strings.Contains(string(encoded), privateDetail) {
				t.Fatal("save feedback exposed private validator detail")
			}
		})
	}
}

func TestAgentSavedPythonFailurePreservesSafeDetailsAndCause(t *testing.T) {
	const secret = "private_source_or_stderr https://example.invalid/secret"
	invalid := agentSavedPythonResultFailure(agentSavedPythonStaticValidation{
		Code: "unresolved_global", Unresolved: []string{secret}, UnresolvedCount: 4, Detail: secret,
	})
	if !errors.Is(invalid, errAgentSavedArtifactPythonInvalid) || strings.Contains(invalid.Error(), secret) {
		t.Fatal("Python classification lost its sentinel or leaked private detail")
	}
	failed := agentSaveArtifactFailure("synthetic.py", fmt.Errorf("outer: %w", invalid))
	if failed["validation_code"] != "unresolved_global" || failed["validation_unresolved_count"] != 4 ||
		failed["validation_stage"] != "python_validation" {
		t.Fatalf("safe Python classification lost structured metadata: %#v", failed)
	}
	missing := &os.PathError{Op: "fork/exec", Path: secret, Err: os.ErrNotExist}
	unavailable := agentSavedPythonValidatorFailure("process_failed", missing)
	var original *os.PathError
	if !errors.Is(unavailable, os.ErrNotExist) || !errors.Is(unavailable, errAgentSavedPythonValidatorUnavailable) ||
		!errors.As(unavailable, &original) || original != missing {
		t.Fatal("validator failure lost its original Is/As cause chain")
	}
	failure := agentSaveArtifactFailure("synthetic.py", unavailable)
	if failure["code"] != "python_validator_unavailable" || failure["validation_code"] != "interpreter_missing" {
		t.Fatalf("missing interpreter was confused with a missing artifact: %#v", failure)
	}
	for _, value := range []any{failed, failure, agentSaveArtifactsCorrectionValue(map[string]any{"errors": []any{failed, failure}})} {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), secret) {
			t.Fatal("safe classification leaked source, process path or diagnostic text")
		}
	}
	if strings.Contains(unavailable.Error(), secret) || strings.Contains(agentSaveArtifactsNoResultsError([]any{failure}).Error(), secret) {
		t.Fatal("failure summary leaked the private cause")
	}
}

func TestAgentSavedPythonFailureKeepsCancellationAndFileOwnership(t *testing.T) {
	for _, test := range []struct {
		name, code string
		err        error
	}{
		{"cancel", "cancelled", agentSavedPythonValidatorFailure("process_failed", context.Canceled)},
		{"deadline", "cancelled", agentSavedPythonValidatorFailure("process_failed", context.DeadlineExceeded)},
		{"cancel_missing", "cancelled", agentSavedPythonValidatorFailure("process_failed", errors.Join(context.Canceled, os.ErrNotExist))},
		{"missing_interpreter", "python_validator_unavailable", agentSavedPythonValidatorFailure("process_failed", &exec.Error{Name: "private-python", Err: exec.ErrNotFound})},
		{"missing_artifact", "file_not_found", &os.PathError{Op: "open", Path: "private-file", Err: os.ErrNotExist}},
		{"unknown", "save_failed", errors.New("private unknown failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := agentSaveArtifactFailure("synthetic.py", test.err)
			if failure["code"] != test.code || failure["retryable"] != false {
				t.Fatalf("classification changed original cancellation/file/retry policy: %#v", failure)
			}
			raw, _ := json.Marshal(failure)
			if strings.Contains(string(raw), "private") {
				t.Fatal("classification echoed raw failure detail")
			}
		})
	}
}

func TestAgentSavedPythonResultClassification(t *testing.T) {
	for _, test := range []struct {
		name, code string
		result     agentSavedPythonStaticValidation
	}{
		{"valid", "", agentSavedPythonStaticValidation{OK: true, Code: "valid_python"}},
		{"syntax", "invalid_python_artifact", agentSavedPythonStaticValidation{Code: "syntax_error", Detail: "private source line"}},
		{"globals", "invalid_python_artifact", agentSavedPythonStaticValidation{Code: "unresolved_global", UnresolvedCount: 2, Unresolved: []string{"private_symbol"}}},
		{"unknown_result", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "private-result-code"}},
		{"contradictory", "python_validator_unavailable", agentSavedPythonStaticValidation{OK: true, Code: "syntax_error"}},
		{"invalid_count", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "unresolved_global", UnresolvedCount: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := agentSavedPythonResultFailure(test.result)
			if test.code == "" {
				if err != nil {
					t.Fatalf("valid result was rejected: %v", err)
				}
				return
			}
			if err == nil || agentSaveArtifactFailure("synthetic.py", err)["code"] != test.code {
				t.Fatalf("invalid result was accepted or misclassified: %v", err)
			}
		})
	}
	for _, code := range []string{"runtime_unavailable", "process_failed", "diagnostic_limit_exceeded", "invalid_result", "private arbitrary code"} {
		err := agentSavedPythonValidatorFailure(code, errors.New("private process detail"))
		failure := agentSaveArtifactFailure("synthetic.py", err)
		raw, _ := json.Marshal(failure)
		if failure["code"] != "python_validator_unavailable" || strings.Contains(string(raw), "private") {
			t.Fatalf("infrastructure classification escaped its allowlist: %#v", failure)
		}
	}
	largeCount := agentSavedPythonResultFailure(agentSavedPythonStaticValidation{Code: "unresolved_global", UnresolvedCount: 1_000_001})
	feedback := agentSaveArtifactFailure("synthetic.py", largeCount)
	if feedback["code"] != "invalid_python_artifact" || feedback["validation_unresolved_count"] != nil {
		t.Fatal("feedback count bound changed validation or exposed an out-of-bound count")
	}
}

func TestAgentSavedPythonInfrastructureDoesNotDemandFileRepair(t *testing.T) {
	failure := agentSaveArtifactFailure("synthetic.py", agentSavedPythonValidatorFailure("runtime_unavailable", nil))
	saved := map[string]any{"artifact_id": "already-saved", "version_id": "immutable-version"}
	result := agentSaveArtifactsCorrectionValue(map[string]any{"artifacts": []any{saved}, "errors": []any{failure}})
	if paths := runnerArtifactSaveFileRepairPaths(result); len(paths) != 0 {
		t.Fatalf("infrastructure failure demanded editing source bytes: %v", paths)
	}
	if result["partial"] != true || result["retryable"] != false || !reflect.DeepEqual(anySliceValue(result["artifacts"]), []any{saved}) {
		t.Fatal("classification changed a successfully saved sibling or retry policy")
	}
	content := agentSaveArtifactFailure("synthetic.py", agentSavedPythonResultFailure(agentSavedPythonStaticValidation{Code: "syntax_error"}))
	result = agentSaveArtifactsCorrectionValue(map[string]any{"errors": []any{content}})
	if paths := runnerArtifactSaveFileRepairPaths(result); !reflect.DeepEqual(paths, []string{"synthetic.py"}) {
		t.Fatalf("actual content error lost the existing file-repair consumer: %v", paths)
	}
}
