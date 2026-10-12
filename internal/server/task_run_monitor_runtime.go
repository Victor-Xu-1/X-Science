package server

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	taskruns "x-science/internal/persistence/taskruns"
)

func (s *Server) autoRepairTaskRun(runID string, message string) (taskruns.Record, bool, error) {
	run, err := s.reconcileTaskRun(runID)
	if err != nil {
		return taskruns.Record{}, false, err
	}
	repaired, queued, err := s.taskRunStore.AutoRepair(run.RunID, message)
	if err != nil {
		return taskruns.Record{}, false, err
	}
	if !queued {
		return repaired, false, nil
	}
	queuedRun, err := s.queueTaskRunSession(repaired, firstNonEmpty(message, "TaskRun auto-repair queued."))
	if err != nil {
		return taskruns.Record{}, false, err
	}
	return queuedRun, true, nil
}

func (s *Server) reconcileTaskRun(runID string) (taskruns.Record, error) {
	run, found, err := s.taskRunStore.Get(runID)
	if err != nil {
		return taskruns.Record{}, err
	}
	if !found {
		return taskruns.Record{}, fmt.Errorf("TaskRun not found: %s", runID)
	}
	if s.sessionStore == nil || s.eventJournal == nil || run.SessionID == "" {
		if err := s.syncTaskRunGoalLedger(run); err != nil {
			return taskruns.Record{}, err
		}
		return run, nil
	}
	session, ok, err := s.sessionStore.Get(run.SessionID)
	if err != nil || !ok || session.Runner == nil {
		if err == nil {
			err = s.syncTaskRunGoalLedger(run)
		}
		return run, err
	}
	assistant, matched, err := s.taskRunTerminalAssistantEvidence(run, session)
	if err != nil {
		return taskruns.Record{}, err
	}
	if !matched {
		return run, s.syncTaskRunGoalLedger(run)
	}
	reconciled, err := s.taskRunStore.ReconcileWithRunnerForInstance(run.RunID, taskruns.ExecutionInstance(run), session.Runner.Status, assistant)
	if err != nil {
		return taskruns.Record{}, err
	}
	if err := s.syncTaskRunGoalLedger(reconciled); err != nil {
		return taskruns.Record{}, err
	}
	return reconciled, nil
}

// Modern terminal state is consumed only through its exact canonical finish.
// Legacy graphs retain their historical tolerant reconciliation behavior.
func (s *Server) taskRunTerminalAssistantEvidence(run taskruns.Record, session sessionstore.Session) (string, bool, error) {
	if taskruns.ExecutionInstance(run) == "" || !isRunnerFinishStatus(session.Runner.Status) {
		assistant, err := s.latestTaskRunAssistantMessage(run.SessionID)
		return assistant, true, err
	}
	entries, err := s.eventJournal.ReadAllStrict(run.SessionID)
	if err != nil {
		return "", false, err
	}
	var finish *eventjournal.Entry
	for index := range entries {
		entry := &entries[index]
		if entry.EventID == session.Runner.LastCheckpointEventID {
			finish = entry
			break
		}
	}
	if finish == nil || stringValue(finish.Message["type"]) != "runner_finished" ||
		stringValue(finish.Message["runnerId"]) != session.Runner.RunnerID || int(numberValue(finish.Message["runnerAttempt"])) != session.Runner.Attempt ||
		stringValue(finish.Message["status"]) != session.Runner.Status || stringValue(finish.Message["taskRunInstanceId"]) != taskruns.ExecutionInstance(run) {
		return "", false, nil
	}
	consumed := numberValue(finish.Message["taskRunInputEventId"])
	if consumed <= 0 {
		return "", false, errors.New("TaskRun terminal receipt has no server-bound input event")
	}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.EventID <= consumed || entry.EventID >= finish.EventID || stringValue(entry.Message["role"]) != "assistant" ||
			stringValue(entry.Message["runnerId"]) != session.Runner.RunnerID || int(numberValue(entry.Message["runnerAttempt"])) != session.Runner.Attempt {
			continue
		}
		if text := stringValue(entry.Message["text"]); strings.TrimSpace(text) != "" {
			return text, true, nil
		}
		if text := stringValue(mapValue(entry.Message["message"])["text"]); strings.TrimSpace(text) != "" {
			return text, true, nil
		}
	}
	return "", true, nil
}

func (s *Server) latestTaskRunAssistantMessage(sessionID string) (string, error) {
	entries, err := s.eventJournal.ReadAll(sessionID)
	if err != nil {
		return "", err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		role, _ := entries[i].Message["role"].(string)
		if role != "assistant" {
			continue
		}
		if text, _ := entries[i].Message["text"].(string); strings.TrimSpace(text) != "" {
			return text, nil
		}
		if message, ok := entries[i].Message["message"].(map[string]any); ok {
			if text, _ := message["text"].(string); strings.TrimSpace(text) != "" {
				return text, nil
			}
		}
	}
	return "", nil
}

func (s *Server) appendTaskRunSystemEvent(run taskruns.Record, text string) error {
	if s.eventJournal == nil || run.SessionID == "" {
		return nil
	}
	_, err := s.eventJournal.Append(run.SessionID, eventjournal.Message{
		"type":    "taskrun_event",
		"role":    "system",
		"text":    text,
		"taskRun": run.RunID,
	}, eventjournal.Metadata{RunID: run.RunID, ClientMessageID: "taskrun-" + run.RunID + "-system-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)})
	return err
}

func (s *Server) mirrorTaskRunRunnerEvent(sessionID string, entry *eventjournal.Entry) error {
	if s == nil || s.taskRunStore == nil || entry == nil || !strings.HasPrefix(sessionID, "taskrun:") {
		return nil
	}
	eventType := strings.TrimSpace(stringValue(entry.Message["type"]))
	if eventType != "runner_checkpoint" && eventType != "runner_finished" {
		return nil
	}
	runID := strings.TrimSpace(entry.RunID)
	if runID == "" {
		runID = strings.TrimPrefix(sessionID, "taskrun:")
	}
	run, found, err := s.taskRunStore.Get(runID)
	if err != nil {
		return err
	}
	if !found || strings.TrimSpace(run.SessionID) != sessionID {
		return errors.New("TaskRun runner event has no matching durable run")
	}
	if eventType == "runner_finished" && strings.TrimSpace(stringValue(entry.Message["taskRunInstanceId"])) != taskruns.ExecutionInstance(run) {
		// Missing IDs are compatible only with a legacy graph. A truthful older
		// finish stays in the session journal but cannot settle a replacement.
		return nil
	}
	now := time.Now().UTC().UnixMilli()
	trace := taskruns.TraceEvent{
		At:      now,
		Event:   eventType,
		StepID:  taskRunRunnerTraceStepID(run, sessionID),
		Message: compactText(firstNonEmpty(stringValue(entry.Message["text"]), "Runner status: "+stringValue(entry.Message["status"])), 500),
		Metadata: map[string]any{
			"sessionId": sessionID,
			"runnerId":  stringValue(entry.Message["runnerId"]),
			"status":    stringValue(entry.Message["status"]),
			"eventId":   entry.EventID,
		},
	}
	for _, key := range []string{"afterEventId", "toolName", "toolCallId", "toolPhase", "resumeCacheKey", "runnerAttempt", "leaseReclaimed", "previousRunnerId"} {
		if value, ok := entry.Message[key]; ok && value != nil {
			trace.Metadata[key] = value
		}
	}
	if calls, ok := entry.Message["modelToolCalls"].([]any); ok {
		trace.Metadata["modelToolCalls"] = len(calls)
	}
	_, err = s.taskRunStore.Update(run.RunID, func(record taskruns.Record) (taskruns.Record, error) {
		if eventType == "runner_finished" && strings.TrimSpace(stringValue(entry.Message["taskRunInstanceId"])) != taskruns.ExecutionInstance(record) {
			return record, nil
		}
		for _, existing := range record.ExecutionTrace {
			if existing.Event != trace.Event || existing.Metadata == nil {
				continue
			}
			if numberValue(existing.Metadata["eventId"]) == entry.EventID {
				return record, nil
			}
		}
		if trace.StepID == "" {
			trace.StepID = taskRunRunnerTraceStepID(record, sessionID)
		}
		record.UpdatedAt = now
		record.ExecutionTrace = append(record.ExecutionTrace, trace)
		record.History = append(record.History, taskruns.HistoryEvent{
			At:      now,
			Event:   eventType,
			Message: trace.Message,
		})
		return record, nil
	})
	if err != nil {
		return err
	}
	return s.appendDelegatedAgentProgressEvent(run, trace, entry)
}

func (s *Server) appendDelegatedAgentProgressEvent(run taskruns.Record, trace taskruns.TraceEvent, childEntry *eventjournal.Entry) error {
	if s == nil || s.eventJournal == nil || childEntry == nil {
		return nil
	}
	parentSessionID := strings.TrimSpace(stringValue(run.Orchestration["parentSessionId"]))
	if parentSessionID == "" || parentSessionID == run.SessionID {
		return nil
	}
	message := eventjournal.Message{
		"type":           "delegated_agent_progress",
		"role":           "system",
		"text":           trace.Message,
		"taskRun":        run.RunID,
		"event":          trace.Event,
		"stepId":         trace.StepID,
		"childSessionId": run.SessionID,
		"childEventId":   childEntry.EventID,
		"runnerId":       stringValue(childEntry.Message["runnerId"]),
		"status":         stringValue(childEntry.Message["status"]),
	}
	for _, key := range []string{"toolName", "toolCallId", "toolPhase", "resumeCacheKey"} {
		if value, ok := trace.Metadata[key]; ok && value != nil {
			message[key] = value
		}
	}
	clientMessageID := fmt.Sprintf("taskrun-%s-parent-progress-%d", run.RunID, childEntry.EventID)
	entry, created, err := s.eventJournal.AppendIdempotent(parentSessionID, message, eventjournal.Metadata{
		RunID:           run.RunID,
		ClientMessageID: clientMessageID,
	})
	if err != nil {
		return err
	}
	if created && entry != nil {
		s.publishSessionEntry(entry)
	}
	return nil
}

func taskRunRunnerTraceStepID(run taskruns.Record, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	for _, child := range run.ActiveChildren {
		if sessionID != "" && child.TaskID != sessionID {
			continue
		}
		if strings.TrimSpace(child.StepID) != "" {
			return child.StepID
		}
	}
	for _, child := range run.ActiveChildren {
		if strings.TrimSpace(child.StepID) != "" {
			return child.StepID
		}
	}
	for _, step := range run.Steps {
		if sessionID != "" && step.ChildTaskID == sessionID && strings.TrimSpace(step.ID) != "" {
			return step.ID
		}
	}
	for _, step := range run.Steps {
		if step.Status == "running" && strings.TrimSpace(step.ID) != "" {
			return step.ID
		}
	}
	return ""
}
