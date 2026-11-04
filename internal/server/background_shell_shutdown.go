package server

import (
	"context"
	"errors"
)

// Admission and shutdown share one lock, so Wait cannot race a new zero-to-one
// lifetime registration. The lifetime includes launch and terminal persistence.
func (s *Server) admitBackgroundShell() bool {
	s.backgroundShellMu.Lock()
	defer s.backgroundShellMu.Unlock()
	if s.backgroundShellClosing {
		return false
	}
	s.backgroundShellWG.Add(1)
	return true
}

func (s *Server) stopAllBackgroundShells(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("background shell shutdown context is required")
	}
	s.backgroundShellMu.Lock()
	s.backgroundShellClosing = true
	if s.backgroundShellDone == nil {
		s.backgroundShellDone = make(chan struct{})
		done := s.backgroundShellDone
		go func() {
			s.backgroundShellWG.Wait()
			close(done)
		}()
	}
	done := s.backgroundShellDone
	taskIDs := make([]string, 0, len(s.backgroundShells))
	for id, entry := range s.backgroundShells {
		if !entry.settling && entry.stopBy == "" {
			entry.stopBy = "server_shutdown"
		}
		taskIDs = append(taskIDs, id)
	}
	s.backgroundShellMu.Unlock()

	var closeErr error
	for _, id := range taskIDs {
		_, err := s.requestBackgroundShellStop(id, backgroundShellStopRequest{by: "server_shutdown", trusted: true})
		closeErr = errors.Join(closeErr, err)
	}
	select {
	case <-ctx.Done():
		return errors.Join(closeErr, ctx.Err())
	case <-done:
		s.backgroundShellMu.Lock()
		defer s.backgroundShellMu.Unlock()
		return errors.Join(closeErr, s.backgroundShellErr)
	}
}
