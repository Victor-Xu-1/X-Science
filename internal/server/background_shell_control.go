package server

import (
	"errors"
	"os"

	"x-science/internal/tools/shellops"
)

var errBackgroundShellCleanupOwner = errors.New("background Shell stop requires the current task owner")

type backgroundShellStopRequest struct {
	by      string
	actor   *backgroundShellOwner
	trusted bool
}

type backgroundShellSettlement struct {
	outputPath string
	result     shellops.Result
	runErr     error
}

// The existing task ID and wait-group lifetime own the attempt before launch.
// A nil RunningCommand here means starting, not stopped or ownerless.
func (s *Server) reserveBackgroundShell(taskID string, owner *backgroundShellOwner) *backgroundShellEntry {
	s.backgroundShellMu.Lock()
	defer s.backgroundShellMu.Unlock()
	if s.backgroundShells == nil {
		s.backgroundShells = map[string]*backgroundShellEntry{}
	}
	entry := &backgroundShellEntry{owner: owner, done: make(chan struct{})}
	if s.backgroundShellClosing {
		entry.stopBy = "server_shutdown"
	}
	s.backgroundShells[taskID] = entry
	return entry
}

func (s *Server) backgroundShellStopRequested(entry *backgroundShellEntry) bool {
	s.backgroundShellMu.Lock()
	defer s.backgroundShellMu.Unlock()
	return entry.stopBy != ""
}

func (s *Server) attachBackgroundShell(taskID string, entry *backgroundShellEntry, running *shellops.RunningCommand) {
	s.backgroundShellMu.Lock()
	entry.RunningCommand = running
	stopBy := entry.stopBy
	s.backgroundShellMu.Unlock()
	if stopBy != "" {
		if _, err := s.requestBackgroundShellStop(taskID, backgroundShellStopRequest{by: stopBy, trusted: true}); err != nil {
			s.backgroundShellMu.Lock()
			s.backgroundShellErr = errors.Join(s.backgroundShellErr, err)
			s.backgroundShellMu.Unlock()
		}
	}
}

// This request does not remove ownership or write a terminal task state.
// Only exact cleanup and the existing terminal writer may do so.
func (s *Server) requestBackgroundShellStop(taskID string, request backgroundShellStopRequest) (*backgroundShellEntry, error) {
	s.backgroundShellMu.Lock()
	entry := s.backgroundShells[taskID]
	if entry == nil || entry.settling {
		s.backgroundShellMu.Unlock()
		return entry, nil
	}
	running, pending := entry.RunningCommand, entry.pending
	if pending != nil && !request.trusted && !entry.owner.matches(request.actor) {
		s.backgroundShellMu.Unlock()
		return entry, errBackgroundShellCleanupOwner
	}
	if entry.stopBy == "" {
		entry.stopBy = request.by
	}
	if pending != nil {
		if entry.recovering {
			s.backgroundShellMu.Unlock()
			return entry, nil
		}
		entry.recovering = true
		s.backgroundShellMu.Unlock()
		err := running.RetryCleanup()
		s.backgroundShellMu.Lock()
		entry.recovering = false
		entry.cleanupErr = err
		if err == nil {
			entry.pending = nil
			entry.settling = true
		}
		s.backgroundShellMu.Unlock()
		if err == nil {
			go s.persistBackgroundShellSettlement(taskID, entry, *pending)
		}
		return entry, err
	}
	if running == nil || entry.killRequested {
		s.backgroundShellMu.Unlock()
		return entry, nil
	}
	entry.killRequested = true
	s.backgroundShellMu.Unlock()
	if err := running.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		s.backgroundShellMu.Lock()
		entry.killRequested = false
		s.backgroundShellMu.Unlock()
		return entry, err
	}
	return entry, nil
}

func (s *Server) settleBackgroundShell(taskID string, entry *backgroundShellEntry, settlement backgroundShellSettlement, cleanupErr error) {
	s.backgroundShellMu.Lock()
	if cleanupErr != nil {
		entry.pending = &settlement
		entry.cleanupErr = cleanupErr
		s.backgroundShellMu.Unlock()
		return
	}
	entry.settling = true
	s.backgroundShellMu.Unlock()
	s.persistBackgroundShellSettlement(taskID, entry, settlement)
}
