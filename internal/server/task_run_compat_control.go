package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	taskruns "x-science/internal/persistence/taskruns"
	"x-science/internal/xsciencelink"
)

func (s *Server) executeTaskRunTool(input map[string]any) (any, error) {
	if err := s.validateRegisteredTool("TaskRun", input); err != nil {
		return nil, err
	}
	if _, present := input["playbook"]; present {
		return nil, errors.New("TaskRun.playbook is retired; provide an explicit task_graph produced by generate_plan")
	}
	if _, present := input["playbook_options"]; present {
		return nil, errors.New("TaskRun.playbook_options is retired; provide an explicit task_graph produced by generate_plan")
	}
	if s.taskRunStore == nil {
		return nil, errors.New("TaskRun store is not configured")
	}
	parsed, err := taskRunInput(input)
	if err != nil {
		return nil, err
	}
	action := strings.TrimSpace(parsed.Action)
	if action == "" {
		action = "start"
	}
	switch action {
	case "list":
		runs, err := s.taskRunStore.List()
		if err != nil {
			return nil, err
		}
		return map[string]any{"runs": runs}, nil
	case "plan":
		run, err := s.taskRunStore.Create(parsed)
		if err != nil {
			return nil, err
		}
		if err := s.syncTaskRunGoalLedger(run); err != nil {
			return nil, err
		}
		return run, nil
	case "start":
		run, err := s.taskRunStore.Create(parsed)
		if err != nil {
			return nil, err
		}
		if executed, completed, err := s.executeTaskRunSystemSteps(run); err != nil {
			return nil, err
		} else if executed {
			if err := s.syncTaskRunGoalLedger(completed); err != nil {
				return nil, err
			}
			return completed, nil
		}
		return s.dispatchTaskRunExternalOrQueue(run, "TaskRun started")
	case "status", "reconcile":
		if strings.TrimSpace(parsed.RunID) == "" {
			return nil, errors.New("TaskRun action=" + action + " requires run_id")
		}
		return s.reconcileTaskRun(parsed.RunID)
	case "resume":
		if strings.TrimSpace(parsed.RunID) == "" {
			return nil, errors.New("TaskRun action=resume requires run_id")
		}
		run, err := s.taskRunStore.Resume(parsed.RunID, parsed.Message)
		if err != nil {
			return nil, err
		}
		return s.dispatchTaskRunExternalOrQueue(run, firstNonEmpty(parsed.Message, "TaskRun resumed"))
	case "advance", "continue", "next":
		if strings.TrimSpace(parsed.RunID) == "" {
			return nil, errors.New("TaskRun action=" + action + " requires run_id")
		}
		return s.advanceTaskRun(parsed.RunID, parsed.Message)
	case "verify":
		if strings.TrimSpace(parsed.RunID) == "" {
			return nil, errors.New("TaskRun action=verify requires run_id")
		}
		if _, err := s.reconcileTaskRun(parsed.RunID); err != nil {
			return nil, err
		}
		run, err := s.taskRunStore.Verify(parsed.RunID)
		if err != nil {
			return nil, err
		}
		if err := s.syncTaskRunGoalLedger(run); err != nil {
			return nil, err
		}
		return run, nil
	case "monitor", "tick":
		return s.monitorTaskRuns(parsed.RunID)
	case "auto_repair", "repair":
		if strings.TrimSpace(parsed.RunID) == "" {
			return nil, errors.New("TaskRun action=" + action + " requires run_id")
		}
		run, _, err := s.autoRepairTaskRun(parsed.RunID, parsed.Message)
		return run, err
	case "cancel":
		if strings.TrimSpace(parsed.RunID) == "" {
			return nil, errors.New("TaskRun action=cancel requires run_id")
		}
		target, found, err := s.taskRunStore.Get(parsed.RunID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("TaskRun not found: %s", parsed.RunID)
		}
		propagation := "requested"
		if taskruns.ExecutionInstance(target) == "" {
			propagation = "unconfirmed"
		}
		result, cancelErr := s.taskRunStore.CancelForInstance(parsed.RunID, taskruns.ExecutionInstance(target), propagation)
		if !result.JSONCommitted {
			return nil, cancelErr
		}
		run := result.Record
		s.stopTaskRunSessionInputs(run)
		_ = s.appendTaskRunSystemEvent(run, "TaskRun cancelled")
		if err := errors.Join(cancelErr, s.syncTaskRunGoalLedger(run)); err != nil {
			return run, err
		}
		return run, nil
	default:
		return nil, fmt.Errorf("unsupported TaskRun action: %s", action)
	}
}

func (s *Server) dispatchTaskRunExternalOrQueue(run taskruns.Record, message string) (taskruns.Record, error) {
	if blocked, handled, err := s.blockTaskRunMissingXScienceLinkClient(run); err != nil {
		return taskruns.Record{}, err
	} else if handled {
		if err := s.syncTaskRunGoalLedger(blocked); err != nil {
			return taskruns.Record{}, err
		}
		return blocked, nil
	}
	if executed, completed, err := s.executeTaskRunXScienceLinkSteps(run); err != nil {
		return taskruns.Record{}, err
	} else if executed {
		if err := s.syncTaskRunGoalLedger(completed); err != nil {
			return taskruns.Record{}, err
		}
		return completed, nil
	}
	return s.queueTaskRunSession(run, message)
}

func (s *Server) blockTaskRunMissingXScienceLinkClient(run taskruns.Record) (taskruns.Record, bool, error) {
	if s == nil || s.taskRunStore == nil {
		return taskruns.Record{}, false, nil
	}
	stepIndex := -1
	clientKind := ""
	for index, step := range run.Steps {
		if step.Status != "pending" && step.Status != "running" {
			continue
		}
		executor := mapValue(step.Executor)
		kind := strings.TrimSpace(stringValue(executor["kind"]))
		if kind == "xsciencelink-browser" {
			stepIndex = index
			clientKind = kind
		}
		break
	}
	if stepIndex < 0 {
		return taskruns.Record{}, false, nil
	}
	gateKind, gateMessage, missingCapabilities, blocked := s.taskRunXScienceLinkGateIssue(run.Steps[stepIndex])
	if !blocked {
		return taskruns.Record{}, false, nil
	}
	updated, err := s.taskRunStore.Update(run.RunID, func(record taskruns.Record) (taskruns.Record, error) {
		now := time.Now().UTC().UnixMilli()
		if stepIndex >= len(record.Steps) {
			return record, nil
		}
		step := &record.Steps[stepIndex]
		executor := mapValue(step.Executor)
		action := strings.TrimSpace(stringValue(executor["action"]))
		message := gateMessage
		blocker := taskruns.Blocker{Kind: gateKind, Message: message, StepID: step.ID, ClientKind: clientKind, MissingCapabilities: missingCapabilities}
		gap := taskruns.CapabilityGap{Kind: gateKind, Message: message, StepID: step.ID, ClientKind: clientKind, MissingCapabilities: missingCapabilities}
		step.Status = "blocked"
		step.Blocker = &blocker
		step.ChildTaskID = ""
		step.OutputPath = ""
		step.UpdatedAt = now
		record.Status = "blocked"
		record.ActiveChildren = []taskruns.ActiveChild{}
		record.Blockers = append(record.Blockers, blocker)
		record.CapabilityGaps = append(record.CapabilityGaps, gap)
		record.NextActions = []string{"Connect or upgrade the X-Science Link browser extension and retry TaskRun action=resume after it advertises the action and capabilities."}
		record.Completion = taskruns.Completion{State: "waiting_user", Verified: false, Confidence: "high", Reason: message, NextAction: "Connect or upgrade X-Science Link, then resume the TaskRun.", UpdatedAt: now}
		record.ExecutionTrace = append(record.ExecutionTrace, taskruns.TraceEvent{At: now, Event: "waiting_for_xsciencelink_client", StepID: step.ID, Message: message, Metadata: map[string]any{"clientKind": clientKind, "action": action}})
		record.UpdatedAt = now
		return record, nil
	})
	if err != nil {
		return taskruns.Record{}, true, err
	}
	return updated, true, nil
}

func (s *Server) taskRunXScienceLinkGateIssue(step taskruns.Step) (string, string, []string, bool) {
	executor := mapValue(step.Executor)
	kind := strings.TrimSpace(stringValue(executor["kind"]))
	action := strings.TrimSpace(stringValue(executor["action"]))
	input := objectMapValue(executor["input"])
	userID := firstNonEmpty(stringValue(input["userId"]), stringValue(input["user_id"]))
	clientID := firstNonEmpty(stringValue(input["clientId"]), stringValue(input["client_id"]))
	clients := s.xscienceLink.ListAllClients()
	if len(clients) == 0 {
		message := "TaskRun requires a connected " + kind + " client before executing X-Science Link action"
		if action != "" {
			message += ": " + action
		}
		return "needsClient", message, nil, true
	}
	if userID == "" || clientID == "" {
		if _, ok := s.selectTaskRunXScienceLinkClient(kind, action); ok {
			return "", "", nil, false
		}
		return "missingCapability", "No connected " + kind + " client advertises X-Science Link action " + action + ".", []string{"action:" + action}, true
	}
	for _, client := range clients {
		if client.ID != clientID || client.UserID != userID {
			continue
		}
		if client.Status != "" && client.Status != "online" {
			return "needsClient", "TaskRun requires online X-Science Link client " + clientID + " before executing action " + action + ".", nil, true
		}
		wantKind := strings.TrimPrefix(kind, "xsciencelink-")
		if wantKind != "" && client.Kind != wantKind {
			return "missingCapability", "X-Science Link client " + clientID + " is " + client.Kind + " but action " + action + " requires " + wantKind + ".", []string{"clientKind:" + wantKind}, true
		}
		if err := xsciencelink.ValidateCommandPolicyWithDefaults(client, action, taskRunXScienceLinkPayload(input), s.xscienceLink.PolicyDefaults()); err != nil {
			return "missingCapability", err.Error(), taskRunXScienceLinkMissingCapabilities(err.Error(), action), true
		}
		return "", "", nil, false
	}
	return "needsClient", "TaskRun requires connected X-Science Link client " + clientID + " for user " + userID + ".", nil, true
}

func taskRunXScienceLinkMissingCapabilities(message string, action string) []string {
	if marker := "missing capability "; strings.Contains(message, marker) {
		after := strings.TrimSpace(message[strings.Index(message, marker)+len(marker):])
		if fields := strings.Fields(after); len(fields) > 0 {
			return []string{fields[0]}
		}
	}
	if strings.Contains(message, "does not support action") && action != "" {
		return []string{"action:" + action}
	}
	return nil
}

func (s *Server) executeTaskRunXScienceLinkSteps(run taskruns.Record) (bool, taskruns.Record, error) {
	if s == nil || s.taskRunStore == nil || s.xscienceLink == nil || len(run.Steps) == 0 {
		return false, taskruns.Record{}, nil
	}
	runnable := make([]taskruns.Step, 0, len(run.Steps))
	for _, step := range run.Steps {
		if step.Status != "pending" && step.Status != "running" {
			continue
		}
		executor := mapValue(step.Executor)
		kind := strings.TrimSpace(stringValue(executor["kind"]))
		if kind != "xsciencelink-browser" {
			return false, taskruns.Record{}, nil
		}
		runnable = append(runnable, step)
	}
	if len(runnable) == 0 {
		return false, taskruns.Record{}, nil
	}
	type stepResult struct {
		stepID   string
		artifact taskruns.Artifact
		evidence taskruns.Evidence
	}
	results := make([]stepResult, 0, len(runnable))
	for _, step := range runnable {
		artifact, evidence, err := s.executeTaskRunXScienceLinkStep(run, step, time.Now().UTC().UnixMilli())
		if err != nil {
			failed, updateErr := s.failTaskRunDirectStep(run.RunID, step.ID, "xsciencelink_step_failed", err.Error())
			if updateErr != nil {
				return true, taskruns.Record{}, updateErr
			}
			return true, failed, nil
		}
		results = append(results, stepResult{stepID: step.ID, artifact: artifact, evidence: evidence})
	}
	updated, err := s.taskRunStore.Update(run.RunID, func(record taskruns.Record) (taskruns.Record, error) {
		now := time.Now().UTC().UnixMilli()
		record.ActiveChildren = []taskruns.ActiveChild{}
		for _, result := range results {
			for i := range record.Steps {
				step := &record.Steps[i]
				if step.ID != result.stepID {
					continue
				}
				step.Status = "completed"
				step.CompletedAt = now
				step.UpdatedAt = now
				step.OutputPath = result.artifact.Path
				step.ChildTaskID = ""
				step.Artifacts = append(step.Artifacts, result.artifact)
				record.Artifacts = append(record.Artifacts, result.artifact)
				record.EvidenceIndex = append(record.EvidenceIndex, result.evidence)
				record.ExecutionTrace = append(record.ExecutionTrace, taskruns.TraceEvent{At: now, Event: "xsciencelink_step_completed", StepID: step.ID, Message: "TaskRun X-Science Link executor completed through connected bridge client.", Metadata: map[string]any{"artifact_path": result.artifact.Path, "artifact_kind": result.artifact.Kind}})
			}
		}
		record.Status = "completed"
		record.NextActions = []string{"TaskRun X-Science Link direct executor completed; verification evidence is recorded."}
		record.Completion = taskruns.Completion{State: "pending_self_check", Verified: false, Confidence: "medium", Reason: "X-Science Link direct executor completed; final verification is pending.", NextAction: "Call TaskRun action=verify.", UpdatedAt: now}
		for i := range record.Acceptance {
			record.Acceptance[i].Status = "passed"
			record.Acceptance[i].UpdatedAt = now
			if record.Acceptance[i].EvidencePath == "" && len(record.Artifacts) > 0 {
				record.Acceptance[i].EvidencePath = record.Artifacts[0].Path
			}
		}
		record.UpdatedAt = now
		record.History = append(record.History, taskruns.HistoryEvent{At: now, Event: "xsciencelink_steps_completed", Message: "TaskRun X-Science Link executor completed."})
		return record, nil
	})
	if err != nil {
		return true, taskruns.Record{}, err
	}
	verified, err := s.taskRunStore.Verify(updated.RunID)
	return true, verified, err
}

func (s *Server) executeTaskRunXScienceLinkStep(record taskruns.Record, step taskruns.Step, now int64) (taskruns.Artifact, taskruns.Evidence, error) {
	executor := mapValue(step.Executor)
	kind := strings.TrimSpace(stringValue(executor["kind"]))
	action := strings.TrimSpace(stringValue(executor["action"]))
	if action == "" {
		return taskruns.Artifact{}, taskruns.Evidence{}, errors.New("X-Science Link TaskRun executor requires action")
	}
	input := objectMapValue(executor["input"])
	userID := firstNonEmpty(stringValue(input["userId"]), stringValue(input["user_id"]))
	clientID := firstNonEmpty(stringValue(input["clientId"]), stringValue(input["client_id"]))
	if userID == "" || clientID == "" {
		selected, ok := s.selectTaskRunXScienceLinkClient(kind, action)
		if !ok {
			return taskruns.Artifact{}, taskruns.Evidence{}, fmt.Errorf("TaskRun requires a connected %s client that supports %s", kind, action)
		}
		userID = firstNonEmpty(userID, selected.UserID)
		clientID = firstNonEmpty(clientID, selected.ID)
	}
	payload := taskRunXScienceLinkPayload(input)
	timeout := time.Duration(numberValue(input["timeout_ms"])) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(numberValue(input["timeoutSeconds"])) * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command, resultCh, err := s.xscienceLink.SendCommand(ctx, userID, clientID, action, payload)
	if err != nil {
		return taskruns.Artifact{}, taskruns.Evidence{}, err
	}
	var result xsciencelink.CommandResult
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		return taskruns.Artifact{}, taskruns.Evidence{}, fmt.Errorf("X-Science Link TaskRun command %s timed out: %w", command.ID, ctx.Err())
	}
	if !result.OK {
		return taskruns.Artifact{}, taskruns.Evidence{}, fmt.Errorf("X-Science Link TaskRun command %s failed: %s", command.ID, result.Error)
	}
	body, err := json.MarshalIndent(map[string]any{
		"executor":  kind,
		"action":    action,
		"userId":    userID,
		"clientId":  clientID,
		"commandId": command.ID,
		"payload":   payload,
		"result":    result.Value,
	}, "", "  ")
	if err != nil {
		return taskruns.Artifact{}, taskruns.Evidence{}, err
	}
	dir, err := s.storageDirectory("taskArtifacts", record.RunID)
	if err != nil {
		return taskruns.Artifact{}, taskruns.Evidence{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return taskruns.Artifact{}, taskruns.Evidence{}, err
	}
	path := filepath.Join(dir, safeTaskRunArtifactName(step.ID, kind)+".json")
	if err := ensurePathWithinRoot(s.fileRoot, path, "taskrun xsciencelink artifact"); err != nil {
		return taskruns.Artifact{}, taskruns.Evidence{}, err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return taskruns.Artifact{}, taskruns.Evidence{}, err
	}
	artifact := taskruns.Artifact{StepID: step.ID, Kind: kind, Path: path, Description: "TaskRun X-Science Link bridge result.", Metadata: map[string]any{"action": action, "clientId": clientID, "commandId": command.ID, "bytes": len(body)}}
	evidence := taskruns.Evidence{ID: fmt.Sprintf("evidence-%d-%s", now, step.ID), StepID: step.ID, Kind: kind, Path: path, Summary: "TaskRun executed X-Science Link action " + action + " through client " + clientID + ".", ProducedAt: now, Metadata: map[string]any{"action": action, "clientId": clientID, "commandId": command.ID}}
	return artifact, evidence, nil
}

func (s *Server) selectTaskRunXScienceLinkClient(kind string, action string) (xsciencelink.Client, bool) {
	wantKind := strings.TrimPrefix(kind, "xsciencelink-")
	for _, client := range s.xscienceLink.ListAllClients() {
		if client.Status != "" && client.Status != "online" {
			continue
		}
		if wantKind != "" && client.Kind != wantKind {
			continue
		}
		if err := xsciencelink.ValidateCommandPolicyWithDefaults(client, action, map[string]any{}, s.xscienceLink.PolicyDefaults()); err == nil {
			return client, true
		}
	}
	return xsciencelink.Client{}, false
}

func taskRunXScienceLinkPayload(input map[string]any) map[string]any {
	payload := make(map[string]any, len(input))
	for key, value := range input {
		switch key {
		case "userId", "user_id", "clientId", "client_id", "timeout_ms", "timeoutSeconds":
			continue
		default:
			payload[key] = value
		}
	}
	return payload
}

func (s *Server) failTaskRunDirectStep(runID string, stepID string, kind string, message string) (taskruns.Record, error) {
	return s.taskRunStore.Update(runID, func(record taskruns.Record) (taskruns.Record, error) {
		now := time.Now().UTC().UnixMilli()
		blocker := taskruns.Blocker{Kind: kind, Message: message, StepID: stepID}
		for i := range record.Steps {
			if record.Steps[i].ID != stepID {
				continue
			}
			record.Steps[i].Status = "failed"
			record.Steps[i].Error = message
			record.Steps[i].Blocker = &blocker
			record.Steps[i].UpdatedAt = now
		}
		record.Status = "failed"
		record.ActiveChildren = []taskruns.ActiveChild{}
		record.Blockers = append(record.Blockers, blocker)
		record.NextActions = []string{"Inspect the failed direct TaskRun executor step and resume after fixing the bridge/tool input."}
		record.Completion = taskruns.Completion{State: "needs_attention", Verified: false, Confidence: "high", Reason: message, UpdatedAt: now}
		record.ExecutionTrace = append(record.ExecutionTrace, taskruns.TraceEvent{At: now, Event: "direct_step_failed", StepID: stepID, Message: message, Metadata: map[string]any{"kind": kind}})
		record.UpdatedAt = now
		return record, nil
	})
}
