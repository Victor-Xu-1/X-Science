package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAgentSavedPythonFailureClassifiesContentAndUnavailable(t *testing.T) {
	const privateDetail = "private source https://example.invalid/secret"
	var unavailable *Server
	// The nil-manager owner returns before seeking or reading this placeholder.
	// No interpreter, file IO or source execution is needed for this regression.
	_, missingRuntime := unavailable.validateAgentSavedPythonArtifact(context.Background(), "synthetic.py", &os.File{})
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
	_, invalid := classifyAgentSavedPythonResult(agentSavedPythonStaticValidation{
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
		analysis   agentSavedPythonAnalysis
	}{
		{"valid", "", agentSavedPythonStaticValidation{OK: true, Code: "valid_python"}, agentSavedPythonValid},
		{"syntax", "invalid_python_artifact", agentSavedPythonStaticValidation{Code: "syntax_error", Detail: "private source line"}, agentSavedPythonValid},
		{"globals", "invalid_python_artifact", agentSavedPythonStaticValidation{Code: "unresolved_global", UnresolvedCount: 2, Unresolved: []string{"private_symbol"}}, agentSavedPythonValid},
		{"indeterminate", "", agentSavedPythonStaticValidation{Code: "analysis_indeterminate", IndeterminateCount: 1, Indeterminate: []string{"private_symbol"}}, agentSavedPythonIndeterminate},
		{"mixed", "invalid_python_artifact", agentSavedPythonStaticValidation{Code: "unresolved_global", UnresolvedCount: 1, IndeterminateCount: 1}, agentSavedPythonValid},
		{"unknown_result", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "private-result-code"}, agentSavedPythonValid},
		{"contradictory", "python_validator_unavailable", agentSavedPythonStaticValidation{OK: true, Code: "syntax_error"}, agentSavedPythonValid},
		{"invalid_count", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "unresolved_global", UnresolvedCount: -1}, agentSavedPythonValid},
		{"empty_indeterminate", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "analysis_indeterminate"}, agentSavedPythonValid},
		{"indeterminate_ok", "python_validator_unavailable", agentSavedPythonStaticValidation{OK: true, Code: "analysis_indeterminate", IndeterminateCount: 1}, agentSavedPythonValid},
		{"indeterminate_missing", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "analysis_indeterminate", IndeterminateCount: 1, UnresolvedCount: 1}, agentSavedPythonValid},
		{"indeterminate_invalid_count", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "analysis_indeterminate", IndeterminateCount: -1}, agentSavedPythonValid},
		{"indeterminate_list_count", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "analysis_indeterminate", IndeterminateCount: 1, Indeterminate: []string{"a", "b"}}, agentSavedPythonValid},
		{"indeterminate_list_bound", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "analysis_indeterminate", IndeterminateCount: 33, Indeterminate: make([]string, 33)}, agentSavedPythonValid},
		{"valid_unknown", "python_validator_unavailable", agentSavedPythonStaticValidation{OK: true, Code: "valid_python", IndeterminateCount: 1}, agentSavedPythonValid},
		{"syntax_unknown", "python_validator_unavailable", agentSavedPythonStaticValidation{Code: "syntax_error", IndeterminateCount: 1}, agentSavedPythonValid},
	} {
		t.Run(test.name, func(t *testing.T) {
			analysis, err := classifyAgentSavedPythonResult(test.result)
			if test.code == "" {
				if err != nil || analysis != test.analysis {
					t.Fatalf("analysis result was rejected or reclassified: %v, %v", analysis, err)
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
	_, largeCount := classifyAgentSavedPythonResult(agentSavedPythonStaticValidation{Code: "unresolved_global", UnresolvedCount: 1_000_001})
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
	_, invalid := classifyAgentSavedPythonResult(agentSavedPythonStaticValidation{Code: "syntax_error"})
	content := agentSaveArtifactFailure("synthetic.py", invalid)
	result = agentSaveArtifactsCorrectionValue(map[string]any{"errors": []any{content}})
	if paths := runnerArtifactSaveFileRepairPaths(result); !reflect.DeepEqual(paths, []string{"synthetic.py"}) {
		t.Fatalf("actual content error lost the existing file-repair consumer: %v", paths)
	}
}

func TestAgentSavedPythonIndeterminatePublicationProtocol(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("the targeted Python publication protocol requires python3")
	}
	fixture := newAgentSaveArtifactsFixture(t)
	const source = "provider = 'def private_symbol(): return 1'\nexec(provider, globals())\nprint(private_symbol())\n"
	write := writeAgentSaveArtifactsFile(t, fixture.projectPath, "synthetic.py", source)
	fixture.saveExecution(t, fixture.identity.access, fixture.projectPath, "exec-python", 1, write)
	input := map[string]any{
		"files": []any{"synthetic.py"}, "language": "python",
		"destination":       map[string]any{"synthetic.py": "working_data"},
		"human_description": "Saving a synthetic source file",
	}
	// This private dependency seam exercises the real stdlib analyzer and closed
	// classifier, then the same store/receipt path as the public tool entry.
	// It does not certify installed managed-kernel discovery or execution.
	validate := func(ctx context.Context, path string, snapshot *os.File) (agentSavedPythonAnalysis, error) {
		if filepath.Ext(path) != ".py" {
			t.Fatal("unexpected validation path")
		}
		if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
			return agentSavedPythonValid, err
		}
		result, err := validateAgentSavedPythonReader(ctx, python, snapshot)
		if _, seekErr := snapshot.Seek(0, io.SeekStart); seekErr != nil {
			return agentSavedPythonValid, errors.Join(err, seekErr)
		}
		if err != nil {
			return agentSavedPythonValid, err
		}
		return classifyAgentSavedPythonResult(result)
	}
	ctx := fixture.toolContext(t, "python-new", input)
	digest := sha256.Sum256([]byte(source))
	wantSHA := hex.EncodeToString(digest[:])
	versionID := ""
	for _, phase := range []string{"new_write", "same_call_replay", "unchanged_replay"} {
		t.Run(phase, func(t *testing.T) {
			callID := "python-new"
			if phase == "unchanged_replay" {
				callID = "python-unchanged"
				ctx = fixture.toolContext(t, callID, input)
			}
			result, err := fixture.server.executeAgentSaveArtifactsWithPythonValidator(ctx, fixture.identity, callID, input, validate)
			if err != nil || result["errors"] != nil || result["code"] != nil || len(runnerArtifactSaveFileRepairPaths(result)) != 0 {
				t.Fatalf("uncertainty entered the correction path: result=%#v err=%v", result, err)
			}
			artifacts := agentSaveArtifactResults(t, result)
			if len(artifacts) != 1 || artifacts[0]["checksum"] != wantSHA || artifacts[0]["version_number"] != 1 {
				t.Fatalf("saved source identity changed: %#v", artifacts)
			}
			savedVersion := stringValue(artifacts[0]["version_id"])
			if phase == "new_write" {
				versionID = savedVersion
			} else if savedVersion != versionID || artifacts[0]["unchanged"] != true {
				t.Fatalf("replay advanced the immutable version: %#v", artifacts)
			}
			warnings := anySliceValue(result["warnings"])
			if len(warnings) != 1 {
				t.Fatalf("publication did not retain one warning: %#v", result)
			}
			warning := mapValue(warnings[0])
			if warning["code"] != "python_static_analysis_indeterminate" || warning["version_id"] != versionID || warning["sha256"] != wantSHA ||
				warning["validation_code"] != "analysis_indeterminate" || !strings.Contains(stringValue(warning["message"]), "not execution certification") {
				t.Fatalf("warning lost its saved-version boundary: %#v", warning)
			}
			raw, _ := json.Marshal(warnings)
			if strings.Contains(string(raw), "private_symbol") || strings.Contains(string(raw), fixture.projectPath) || strings.Contains(string(raw), "repair") {
				t.Fatal("warning exposed private symbols, host paths or a repair instruction")
			}
			_, version, reader, found, err := fixture.store.OpenArtifactVersionContent(versionID)
			if err != nil || !found {
				t.Fatalf("immutable source missing: found=%t err=%v", found, err)
			}
			content, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || string(content) != source || version.ContentSHA256 != wantSHA {
				t.Fatalf("publication changed source bytes: read=%v close=%v", readErr, closeErr)
			}
			logs, err := fixture.store.ListExecutionLog("frame-save", versionID)
			if err != nil || len(logs) != 1 || logs[0].ID != "exec-python" {
				t.Fatalf("save lost the real receipt: %#v err=%v", logs, err)
			}
		})
	}
	for _, test := range []struct{ name, source, code string }{
		{"definite", "print(definitely_missing)\n", "invalid_python_artifact"},
		{"mixed", "print(definitely_missing)\nprovider = 'x=1'\nexec(provider, globals())\nprint(unknown)\n", "invalid_python_artifact"},
		{"syntax", "def broken(:\n", "invalid_python_artifact"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeAgentSaveArtifactsFile(t, fixture.projectPath, "synthetic.py", test.source)
			callID := "python-" + test.name
			ctx := fixture.toolContext(t, callID, input)
			result, err := fixture.server.executeAgentSaveArtifactsWithPythonValidator(ctx, fixture.identity, callID, input, validate)
			failures := anySliceValue(result["errors"])
			if err == nil || len(agentSaveArtifactResults(t, result)) != 0 || len(failures) != 1 || mapValue(failures[0])["code"] != test.code || result["warnings"] != nil {
				t.Fatalf("definite invalid source was published: %#v err=%v", result, err)
			}
			_, current, found, err := fixture.store.GetCurrentArtifactVersionMetadata(agentSavedArtifactID(fixture.stream.UID, "synthetic.py"))
			if err != nil || !found || current.ID != versionID || current.ContentSHA256 != wantSHA {
				t.Fatalf("rejection changed the saved source: %#v found=%t err=%v", current, found, err)
			}
		})
	}
	t.Run("protocol_failure", func(t *testing.T) {
		writeAgentSaveArtifactsFile(t, fixture.projectPath, "synthetic.py", source)
		const callID = "python-protocol-failure"
		ctx := fixture.toolContext(t, callID, input)
		result, err := fixture.server.executeAgentSaveArtifactsWithPythonValidator(ctx, fixture.identity, callID, input,
			func(context.Context, string, *os.File) (agentSavedPythonAnalysis, error) {
				return classifyAgentSavedPythonResult(agentSavedPythonStaticValidation{Code: "unrecognized", IndeterminateCount: 1})
			})
		failures := anySliceValue(result["errors"])
		if err == nil || len(failures) != 1 || mapValue(failures[0])["code"] != "python_validator_unavailable" ||
			result["warnings"] != nil || len(runnerArtifactSaveFileRepairPaths(result)) != 0 {
			t.Fatalf("unknown protocol acquired publication or source-repair authority: %#v err=%v", result, err)
		}
	})
	t.Run("cancel_before_persistence", func(t *testing.T) {
		// A real indeterminate result is insufficient to issue a warning: the
		// canonical association must still succeed for this exact invocation.
		const callID = "python-cancel-write"
		ctx, cancel := context.WithCancel(fixture.toolContext(t, callID, input))
		defer cancel()
		result, err := fixture.server.executeAgentSaveArtifactsWithPythonValidator(ctx, fixture.identity, callID, input,
			func(ctx context.Context, path string, snapshot *os.File) (agentSavedPythonAnalysis, error) {
				analysis, err := validate(ctx, path, snapshot)
				cancel()
				return analysis, err
			})
		if err == nil || len(agentSaveArtifactResults(t, result)) != 0 || result["warnings"] != nil {
			t.Fatalf("failed persistence acquired a saved-version warning: %#v err=%v", result, err)
		}
	})
}
