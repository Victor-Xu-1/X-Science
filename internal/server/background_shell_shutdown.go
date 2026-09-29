package server

import (
	"context"
	"errors"
	"os"

	"synon-go/internal/tools/shellops"
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
	running := make([]*shellops.RunningCommand, 0, len(s.backgroundShells))
	for _, command := range s.backgroundShells {
		running = append(running, command)
	}
	s.backgroundShellMu.Unlock()

	var closeErr error
	for _, command := range running {
		if command != nil {
			if err := command.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				closeErr = errors.Join(closeErr, err)
			}
		}
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
