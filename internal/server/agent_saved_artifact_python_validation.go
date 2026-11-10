package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed agent_saved_artifact_python_validator.py
var agentSavedPythonStaticValidator string

type agentSavedPythonStaticValidation struct {
	OK                 bool     `json:"ok"`
	Code               string   `json:"code"`
	Unresolved         []string `json:"unresolved"`
	UnresolvedCount    int      `json:"unresolved_count"`
	Indeterminate      []string `json:"indeterminate"`
	IndeterminateCount int      `json:"indeterminate_count"`
	Detail             string   `json:"detail,omitempty"`
}

type agentSavedPythonAnalysis uint8

const (
	agentSavedPythonValid agentSavedPythonAnalysis = iota
	agentSavedPythonIndeterminate
)

type agentSavedPythonValidator func(context.Context, string, *os.File) (agentSavedPythonAnalysis, error)

func (s *Server) validateAgentSavedPythonArtifact(ctx context.Context, relativePath string, snapshot *os.File) (agentSavedPythonAnalysis, error) {
	if !strings.EqualFold(filepath.Ext(strings.TrimSpace(relativePath)), ".py") || snapshot == nil {
		return agentSavedPythonValid, nil
	}
	if s == nil || s.kernelManager == nil {
		return agentSavedPythonValid, agentSavedPythonValidatorFailure("runtime_unavailable", nil)
	}
	python, _, _, _, err := s.kernelManager.ScientificArtifactValidator()
	if err != nil || strings.TrimSpace(python) == "" {
		return agentSavedPythonValid, agentSavedPythonValidatorFailure("runtime_unavailable", err)
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

func validateAgentSavedPythonReader(ctx context.Context, python string, source io.Reader) (agentSavedPythonStaticValidation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.Command(python, "-I", "-c", agentSavedPythonStaticValidator)
	command.Stdin = &contextReader{ctx: ctx, reader: source}
	command.Env = []string{"LANG=C.UTF-8", "PYTHONNOUSERSITE=1", "PYTHONUNBUFFERED=1"}
	output := &cappedScientificOutput{limit: maxScientificArtifactValidatorOutput}
	diagnostic := &cappedScientificOutput{limit: maxScientificArtifactValidatorOutput}
	command.Stdout, command.Stderr = output, diagnostic
	err := runArtifactValidationCommand(ctx, command, 0)
	if ctx.Err() != nil {
		return agentSavedPythonStaticValidation{}, ctx.Err()
	}
	if err != nil {
		return agentSavedPythonStaticValidation{}, agentSavedPythonValidatorFailure("process_failed", err)
	}
	if output.overflow || diagnostic.overflow {
		return agentSavedPythonStaticValidation{}, agentSavedPythonValidatorFailure("diagnostic_limit_exceeded", nil)
	}
	var result agentSavedPythonStaticValidation
	if err := json.Unmarshal([]byte(output.String()), &result); err != nil {
		return agentSavedPythonStaticValidation{}, agentSavedPythonValidatorFailure("invalid_result", err)
	}
	return result, nil
}
