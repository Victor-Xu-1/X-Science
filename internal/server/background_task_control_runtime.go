package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	taskstore "x-science/internal/persistence/tasks"
)

func (s *Server) executeTaskOutputTool(ctx context.Context, toolName string, input map[string]any) (any, error) {
	if err := s.validateRegisteredTool(toolName, input); err != nil {
		return nil, err
	}
	if s.taskStore == nil {
		return map[string]any{"retrieval_status": "missing", "task": nil}, nil
	}
	taskID := strings.TrimSpace(stringValue(input["task_id"]))
	if taskID == "" {
		return nil, errors.New("TaskOutput.task_id is required")
	}
	block := boolValue(input["block"], false)
	timeoutMs := numberValue(input["timeout"])
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	if timeoutMs > 600000 {
		timeoutMs = 600000
	}
	task, found, err := s.taskStore.Get(taskID)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]any{"retrieval_status": "missing", "task": nil}, nil
	}
	cleanupDenied := false
	snapshot := func(task taskstore.Task) taskOutputState {
		state := s.taskOutputState(ctx, task, cleanupDenied)
		cleanupDenied = cleanupDenied || state.cleanupDenied
		return state
	}
	state := snapshot(task)
	if !block || state.cleanupErr != nil {
		return state.result(task), nil
	}
	deadline := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for state.status == "not_ready" && state.cleanupErr == nil {
		select {
		case <-ctx.Done():
			state = snapshot(task)
			state.status = "timeout"
			return state.result(task), nil
		case <-deadline.C:
			state = snapshot(task)
			if state.cleanupErr == nil {
				s.observeTaskOutputPendingWait(ctx, taskID)
				state.status = "timeout"
			}
			return state.result(task), nil
		case <-ticker.C:
			nextTask, nextFound, err := s.taskStore.Get(taskID)
			if err != nil {
				return nil, err
			}
			if !nextFound {
				return map[string]any{"retrieval_status": "missing", "task": nil}, nil
			}
			task = nextTask
			state = snapshot(task)
		}
	}
	return state.result(task), nil
}

func (s *Server) executeTaskStopTool(ctx context.Context, toolName string, input map[string]any) (any, error) {
	if err := s.validateRegisteredTool(toolName, input); err != nil {
		return nil, err
	}
	if s.taskStore == nil {
		return nil, errors.New("task store is not configured")
	}
	taskID := strings.TrimSpace(stringValue(input["task_id"]))
	if taskID == "" {
		taskID = strings.TrimSpace(stringValue(input["shell_id"]))
	}
	if taskID == "" {
		return nil, errors.New("TaskStop.task_id is required")
	}
	task, found, err := s.taskStore.Get(taskID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("No task found with ID: %s", taskID)
	}
	request := backgroundShellStopRequest{by: "TaskStop", actor: s.currentBackgroundShellOwner(ctx)}
	if entry, stopErr := s.requestBackgroundShellStop(taskID, request); entry != nil {
		if errors.Is(stopErr, errBackgroundShellCleanupOwner) {
			return nil, stopErr
		}
		if stopErr != nil {
			return nil, fmt.Errorf("background Shell task %s has unconfirmed stop or cleanup: %w", taskID, stopErr)
		}
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		select {
		case <-entry.done:
			if entry.settlementErr != nil {
				return nil, entry.settlementErr
			}
		case <-stopCtx.Done():
			return nil, fmt.Errorf("background Shell task %s stop is not yet confirmed: %w", taskID, stopCtx.Err())
		}
		stopped, exists, err := s.taskStore.Get(taskID)
		if err != nil {
			return nil, err
		}
		if !exists || stopped.Status != "stopped" {
			return nil, fmt.Errorf("background Shell task %s already settled without a stop", taskID)
		}
		return stoppedTaskResult(stopped), nil
	}
	if !taskStopCanStop(task.Status) {
		return nil, fmt.Errorf("Task %s is not running (status: %s)", taskID, task.Status)
	}
	// Metadata cannot authorize stopping a process. It can identify a legacy
	// Shell row whose missing in-memory owner makes physical stop unprovable.
	if stringValue(task.Metadata["task_type"]) == "shell_command" {
		return nil, fmt.Errorf("background Shell task %s has no registered process owner", taskID)
	}
	stoppedStatus := "stopped"
	metadata := cloneTaskMetadata(task.Metadata)
	metadata["stoppedBy"] = "TaskStop"
	metadata["stoppedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	stoppedTask, _, _, err := s.taskStore.UpdateWithOptions(taskID, taskstore.UpdateOptions{
		Status:      &stoppedStatus,
		Metadata:    metadata,
		MetadataSet: true,
	})
	if err != nil {
		return nil, err
	}
	return stoppedTaskResult(stoppedTask), nil
}

func stoppedTaskResult(task taskstore.Task) map[string]any {
	command := taskStopCommand(task)
	return map[string]any{
		"message":   fmt.Sprintf("Successfully stopped task: %s (%s)", task.ID, command),
		"task_id":   task.ID,
		"task_type": "go_task",
		"command":   command,
	}
}

func taskStopCanStop(status string) bool {
	switch status {
	case "running":
		return true
	default:
		return false
	}
}

func taskStopCommand(task taskstore.Task) string {
	if value, ok := task.Metadata["command"].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return firstNonEmpty(task.ActiveForm, task.Description, task.Subject, task.Title)
}

func taskOutputStillRunning(status string) bool {
	switch status {
	case "pending", "running", "in_progress", "open":
		return true
	default:
		return false
	}
}

type taskOutputState struct {
	status        string
	cleanupErr    error
	cleanupDenied bool
}

func (s *Server) taskOutputState(ctx context.Context, task taskstore.Task, cleanupDenied bool) taskOutputState {
	s.backgroundShellMu.Lock()
	entry := s.backgroundShells[task.ID]
	var cleanupErr error
	if !cleanupDenied && entry != nil && entry.cleanupErr != nil {
		// Resolving a runner claim can read stores. Take the diagnostic and
		// owner from the same registry state after reacquiring the lock.
		s.backgroundShellMu.Unlock()
		current := s.currentBackgroundShellOwner(ctx)
		s.backgroundShellMu.Lock()
		entry = s.backgroundShells[task.ID]
		if entry != nil && entry.cleanupErr != nil {
			if entry.owner.matches(current) {
				cleanupErr = entry.cleanupErr
			} else {
				cleanupDenied = true
			}
		}
	}
	s.backgroundShellMu.Unlock()
	if entry != nil {
		return taskOutputState{status: "not_ready", cleanupErr: cleanupErr, cleanupDenied: cleanupDenied}
	}
	if taskOutputStillRunning(task.Status) {
		return taskOutputState{status: "not_ready", cleanupDenied: cleanupDenied}
	}
	return taskOutputState{status: "success", cleanupDenied: cleanupDenied}
}

func (state taskOutputState) result(task taskstore.Task) map[string]any {
	return map[string]any{"retrieval_status": state.status, "task": taskOutputPayload(task, state.cleanupErr)}
}

func taskOutputPayload(task taskstore.Task, cleanupErr error) map[string]any {
	outputPath := "tasks.json"
	if value, ok := task.Metadata["output_path"].(string); ok && strings.TrimSpace(value) != "" {
		outputPath = value
	}
	output := ""
	if value, ok := task.Metadata["output"].(string); ok {
		output = value
	}
	if strings.TrimSpace(output) == "" {
		output = task.Description
	}
	if strings.TrimSpace(output) == "" {
		encoded, err := json.MarshalIndent(task, "", "  ")
		if err == nil {
			output = string(encoded)
		}
	}
	payload := map[string]any{
		"task_id":     task.ID,
		"task_type":   "go_task",
		"status":      task.Status,
		"description": firstNonEmpty(task.Description, task.Subject, task.Title),
		"output_path": outputPath,
		"output":      output,
	}
	if value, ok := task.Metadata["exitCode"]; ok {
		payload["exitCode"] = value
	}
	if value, ok := task.Metadata["error"].(string); ok && strings.TrimSpace(value) != "" {
		payload["error"] = value
	}
	if cleanupErr != nil {
		payload["cleanupPending"] = true
		payload["error"] = cleanupErr.Error()
	}
	return payload
}
