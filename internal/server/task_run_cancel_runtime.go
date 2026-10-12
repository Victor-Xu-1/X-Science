package server

import (
	"fmt"
	"strings"

	taskruns "x-science/internal/persistence/taskruns"
)

func taskRunOwnedChildSessionIDs(run taskruns.Record) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(run.ActiveChildren))
	for _, child := range run.ActiveChildren {
		sessionID := strings.TrimSpace(child.TaskID)
		if sessionID == "" {
			continue
		}
		if _, ok := seen[sessionID]; ok {
			continue
		}
		seen[sessionID] = struct{}{}
		ids = append(ids, sessionID)
	}
	return ids
}

func (s *Server) stopTaskRunOwnedChildExecutions(sessionIDs []string, reason string) {
	if s == nil || len(sessionIDs) == 0 {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "TaskRun cancelled"
	}
	for _, sessionID := range sessionIDs {
		s.stopTaskRunOwnedChildExecution(sessionID, reason)
	}
}

func (s *Server) stopTaskRunOwnedChildExecution(sessionID, reason string) (bool, string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, ""
	}
	expectedRunnerID := ""
	if s.sessionStore != nil {
		session, ok, err := s.sessionStore.Get(sessionID)
		if err == nil && ok && session.Runner != nil {
			expectedRunnerID = strings.TrimSpace(session.Runner.RunnerID)
		}
	}
	s.sessionRunsMu.Lock()
	active := s.sessionRuns[sessionID]
	s.sessionRunsMu.Unlock()
	if active == nil {
		return false, ""
	}
	if expectedRunnerID != "" && active.runnerID != expectedRunnerID {
		return false, active.runnerID
	}
	return s.stopActiveSessionRun(sessionID, reason)
}

func (s *Server) cancelTaskRun(runID string) (taskruns.Record, error) {
	run, found, err := s.taskRunStore.Get(runID)
	if err != nil {
		return taskruns.Record{}, err
	}
	if !found {
		return taskruns.Record{}, fmt.Errorf("TaskRun not found: %s", runID)
	}
	childSessions := taskRunOwnedChildSessionIDs(run)
	run, err = s.taskRunStore.Cancel(runID)
	if err != nil {
		return taskruns.Record{}, err
	}
	s.stopTaskRunOwnedChildExecutions(childSessions, "TaskRun cancelled")
	return run, nil
}
