package server

import (
	"context"

	"errors"
	"fmt"
	"log"

	"os"
	osexec "os/exec"
	"path/filepath"

	"strings"

	"time"

	taskstore "synon-go/internal/persistence/tasks"

	"synon-go/internal/executionprep"
	"synon-go/internal/tools/shellops"
)

func (s *Server) executeShellTool(ctx context.Context, toolName string, input map[string]any) (any, error) {
	if err := s.validateRegisteredTool(toolName, input); err != nil {
		return nil, err
	}
	if toolName == "Shell" {
		routeTool, routeShell, routeReason, err := chooseOriginalShellRoute(input)
		if err != nil {
			return nil, err
		}
		command := stringValue(input["command"])
		if err := shellops.CheckSafety(routeTool, command, nil); err != nil {
			return nil, err
		}
		if boolValue(input["run_in_background"], false) {
			return s.startBackgroundShellCommand(routeTool, routeShell, routeReason, input, executionprep.ObservationFromContext(ctx))
		}
		result, err := shellops.ExecuteShellCommand(
			ctx,
			s.fileRoot,
			routeTool,
			command,
			stringValue(input["workdir"]),
			numberValue(input["timeout"]),
		)
		if err != nil {
			return nil, err
		}
		return originalShellResult(result, routeShell, routeReason, ""), nil
	}
	if toolName == "Bash" || toolName == "powershell" {
		executorToolName := toolName
		if toolName == "powershell" {
			executorToolName = "PowerShell"
		}
		if err := shellops.CheckSafety(executorToolName, stringValue(input["command"]), nil); err != nil {
			return nil, err
		}
		if boolValue(input["run_in_background"], false) {
			shellName := strings.ToLower(executorToolName)
			return s.startBackgroundShellCommand(executorToolName, shellName, "", input, executionprep.ObservationFromContext(ctx))
		}
		return shellops.ExecuteShellCommand(
			ctx,
			s.fileRoot,
			executorToolName,
			stringValue(input["command"]),
			stringValue(input["workdir"]),
			numberValue(input["timeout"]),
		)
	}
	if err := shellops.CheckSafety(toolName, stringValue(input["command"]), stringArrayValue(input["args"])); err != nil {
		return nil, err
	}
	return shellops.Execute(
		ctx,
		s.fileRoot,
		stringValue(input["command"]),
		stringArrayValue(input["args"]),
		stringValue(input["workdir"]),
		numberValue(input["timeout"]),
	)
}

func (s *Server) startBackgroundShellCommand(toolName string, shellName string, routeReason string, input map[string]any, observations ...*executionprep.Observation) (map[string]any, error) {
	if s.taskStore == nil {
		return nil, errors.New("task store is not configured")
	}
	if strings.TrimSpace(s.fileRoot) == "" {
		return nil, errors.New("file root is not configured")
	}
	if !s.admitBackgroundShell() {
		return nil, errors.New("background shell service is shutting down")
	}
	settlementStarted := false
	defer func() {
		if !settlementStarted {
			s.backgroundShellWG.Done()
		}
	}()
	command := stringValue(input["command"])
	description := firstNonEmpty(stringValue(input["description"]), command)
	task, err := s.taskStore.CreateWithOptions(taskstore.CreateOptions{
		Subject:     description,
		Description: command,
		ActiveForm:  command,
		Status:      "running",
		Metadata: map[string]any{
			"task_type": "shell_command",
			"command":   command,
			"shell":     shellName,
			"toolName":  toolName,
			"startedAt": time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		return nil, err
	}
	rules, err := s.loadStorageRules()
	if err != nil {
		return nil, err
	}
	outputRel := filepath.ToSlash(filepath.Join(rules.Logs, task.ID+".log"))
	outputPath := filepath.Join(s.fileRoot, outputRel)
	if err := ensurePathWithinRoot(s.fileRoot, outputPath, "background shell output"); err != nil {
		return nil, err
	}
	metadata := cloneTaskMetadata(task.Metadata)
	metadata["output_path"] = outputRel
	if routeReason != "" {
		metadata["routeReason"] = routeReason
	}
	if _, _, _, err := s.taskStore.UpdateWithOptions(task.ID, taskstore.UpdateOptions{Metadata: metadata, MetadataSet: true}); err != nil {
		return nil, err
	}
	executionContext := context.Background()
	if len(observations) > 0 {
		executionContext = executionprep.WithObservation(executionContext, observations[0])
	}
	running, err := shellops.StartShellCommand(executionContext, s.fileRoot, toolName, command, stringValue(input["workdir"]))
	if err != nil {
		failedStatus := "failed"
		metadata["error"] = err.Error()
		_, _, _, _ = s.taskStore.UpdateWithOptions(task.ID, taskstore.UpdateOptions{Status: &failedStatus, Metadata: metadata, MetadataSet: true})
		return nil, err
	}
	s.storeBackgroundShell(task.ID, running)
	settlementStarted = true
	go s.finishBackgroundShell(task.ID, outputPath, running)
	result := map[string]any{
		"backgroundTaskId":          task.ID,
		"task_id":                   task.ID,
		"output_path":               outputRel,
		"status":                    "running",
		"interrupted":               false,
		"backgroundedByUser":        false,
		"assistantAutoBackgrounded": false,
	}
	if shellName != "" {
		result["shell"] = shellName
	}
	if routeReason != "" {
		result["routeReason"] = routeReason
	}
	return result, nil
}

func (s *Server) storeBackgroundShell(taskID string, running *shellops.RunningCommand) {
	s.backgroundShellMu.Lock()
	defer s.backgroundShellMu.Unlock()
	if s.backgroundShells == nil {
		s.backgroundShells = map[string]*shellops.RunningCommand{}
	}
	s.backgroundShells[taskID] = running
	// Shutdown may start while an already admitted launch prepares its process.
	// It remains in the wait group and must be killed before registration returns.
	if s.backgroundShellClosing && running != nil {
		if err := running.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			if s.backgroundShellErr == nil {
				s.backgroundShellErr = err
			}
		}
	}
}

func (s *Server) takeBackgroundShell(taskID string) *shellops.RunningCommand {
	s.backgroundShellMu.Lock()
	defer s.backgroundShellMu.Unlock()
	running := s.backgroundShells[taskID]
	delete(s.backgroundShells, taskID)
	return running
}

func (s *Server) finishBackgroundShell(taskID string, outputPath string, running *shellops.RunningCommand) {
	var settlementErr error
	defer s.backgroundShellWG.Done()
	defer func() {
		s.backgroundShellMu.Lock()
		delete(s.backgroundShells, taskID)
		if s.backgroundShellErr == nil {
			s.backgroundShellErr = settlementErr
		}
		s.backgroundShellMu.Unlock()
		if settlementErr != nil {
			log.Printf("background shell settlement %s: %v", taskID, settlementErr)
		}
	}()
	result, err := running.Wait()
	content := backgroundShellOutputContent(result)
	if writeErr := os.MkdirAll(filepath.Dir(outputPath), 0o700); writeErr != nil {
		settlementErr = writeErr
	} else if writeErr := os.WriteFile(outputPath, []byte(content), 0o600); writeErr != nil {
		settlementErr = writeErr
	}
	if s.taskStore == nil {
		settlementErr = errors.Join(settlementErr, errors.New("background task store is unavailable"))
		return
	}
	current, found, getErr := s.taskStore.Get(taskID)
	if getErr != nil || !found {
		if getErr == nil {
			getErr = errors.New("background task disappeared before settlement")
		}
		settlementErr = errors.Join(settlementErr, getErr)
		return
	}
	// Never hold the admission lock over filesystem I/O: Close must still be
	// able to establish its deadline while persistence is stalled. A command
	// already settled before this snapshot can complete normally; the wait
	// group retains ownership until its terminal write actually finishes.
	s.backgroundShellMu.Lock()
	closing := s.backgroundShellClosing
	s.backgroundShellMu.Unlock()
	status := "completed"
	if err != nil || settlementErr != nil {
		status = "failed"
	}
	if current.Status == "stopped" {
		status = "stopped"
	}
	metadata := cloneTaskMetadata(current.Metadata)
	if closing && current.Status != "stopped" {
		status = "stopped"
		metadata["stoppedBy"] = "server_shutdown"
		metadata["stoppedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	metadata["output"] = content
	metadata["stdout"] = result.Stdout
	metadata["stderr"] = result.Stderr
	metadata["exitCode"] = result.ExitCode
	metadata["stdoutBytes"] = result.StdoutBytes
	metadata["stderrBytes"] = result.StderrBytes
	metadata["completedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	if resultErr := errors.Join(err, settlementErr); resultErr != nil {
		metadata["error"] = resultErr.Error()
	}
	_, _, _, updateErr := s.taskStore.UpdateWithOptions(taskID, taskstore.UpdateOptions{Status: &status, Metadata: metadata, MetadataSet: true})
	settlementErr = errors.Join(settlementErr, updateErr)
}

func backgroundShellOutputContent(result shellops.Result) string {
	parts := []string{}
	if result.Stdout != "" {
		parts = append(parts, result.Stdout)
	}
	if result.Stderr != "" {
		parts = append(parts, result.Stderr)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func cloneTaskMetadata(metadata map[string]any) map[string]any {
	clone := map[string]any{}
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}

func chooseOriginalShellRoute(input map[string]any) (toolName string, shellName string, reason string, err error) {
	requested := strings.ToLower(strings.TrimSpace(stringValue(input["shell"])))
	if requested == "" {
		requested = "auto"
	}
	switch requested {
	case "bash":
		return "Bash", "bash", "requested bash shell", nil
	case "powershell":
		return "PowerShell", "powershell", "requested powershell shell", nil
	case "auto":
		if _, err := osexec.LookPath("bash"); err == nil {
			return "Bash", "bash", "auto selected bash from PATH", nil
		}
		if _, err := osexec.LookPath("pwsh"); err == nil {
			return "PowerShell", "powershell", "auto selected pwsh from PATH", nil
		}
		if _, err := osexec.LookPath("powershell"); err == nil {
			return "PowerShell", "powershell", "auto selected powershell from PATH", nil
		}
		return "", "", "", errors.New("Shell auto routing requires bash, pwsh, or powershell to be installed and available on PATH")
	default:
		return "", "", "", fmt.Errorf("unsupported Shell shell route %q: expected auto, bash, or powershell", requested)
	}
}

func originalShellResult(result shellops.Result, shellName string, routeReason string, normalizedCommand string) map[string]any {
	output := map[string]any{
		"command":         result.Command,
		"args":            result.Args,
		"workdir":         result.Workdir,
		"exitCode":        result.ExitCode,
		"stdout":          result.Stdout,
		"stderr":          result.Stderr,
		"stdoutBytes":     result.StdoutBytes,
		"stderrBytes":     result.StderrBytes,
		"stdoutTruncated": result.StdoutTruncated,
		"stderrTruncated": result.StderrTruncated,
		"shell":           shellName,
		"routeReason":     routeReason,
		"interrupted":     false,
	}
	if strings.TrimSpace(normalizedCommand) != "" {
		output["normalizedCommand"] = normalizedCommand
	}
	return output
}

func (s *Server) executeSleepTool(ctx context.Context, toolName string, input map[string]any) (any, error) {
	if err := s.validateRegisteredTool(toolName, input); err != nil {
		return nil, err
	}
	duration, err := sleepDuration(input)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now().UTC()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		endedAt := time.Now().UTC()
		return map[string]any{
			"requestedMs": duration.Milliseconds(),
			"sleptMs":     endedAt.Sub(startedAt).Milliseconds(),
			"startedAt":   startedAt,
			"endedAt":     endedAt,
			"interrupted": false,
		}, nil
	case <-ctx.Done():
		endedAt := time.Now().UTC()
		result := map[string]any{
			"requestedMs": duration.Milliseconds(),
			"sleptMs":     endedAt.Sub(startedAt).Milliseconds(),
			"startedAt":   startedAt,
			"endedAt":     endedAt,
			"interrupted": true,
			"error":       ctx.Err().Error(),
		}
		if cause := context.Cause(ctx); cause != nil {
			return result, cause
		}
		return result, ctx.Err()
	}
}
