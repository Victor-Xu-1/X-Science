package server

import (
	"net/http"
	"sync"
)

// Long HTTP-owned operations are not chat runs, but must share the same atomic
// idle-reload fence. The request context and existing operation deadline remain
// their lifetime authority; the release closure is idempotent and panic-safe
// when deferred by the handler wrapper. No background lease or fake turn exists.
func (s *Server) beginRuntimeOperation() (func(), error) {
	if s == nil {
		return nil, ErrRuntimeDraining
	}
	s.sessionRunsMu.Lock()
	if s.sessionRunsDraining || s.sessionRunsDrainEvaluating {
		s.sessionRunsMu.Unlock()
		return nil, ErrRuntimeDraining
	}
	s.activeRuntimeOperations++
	s.sessionRunsMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.sessionRunsMu.Lock()
			s.activeRuntimeOperations--
			s.sessionRunsMu.Unlock()
		})
	}, nil
}

func (s *Server) withRuntimeOperation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		finish, err := s.beginRuntimeOperation()
		if err != nil {
			writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{
				"message": "runtime is restarting; retry when ready", "code": "runtime_draining",
			})
			return
		}
		defer finish()
		next(w, r)
	}
}

func (s *Server) runtimeActivity() (sessions, operations int, draining bool) {
	if s == nil {
		return 0, 0, false
	}
	s.sessionRunsMu.Lock()
	defer s.sessionRunsMu.Unlock()
	return len(s.sessionRuns), s.activeRuntimeOperations, s.sessionRunsDraining || s.sessionRunsDrainEvaluating
}

// Requires sessionRunsMu. Both ownership classes block idle admission, while
// health continues to distinguish real chat sessions from HTTP-owned work.
func (s *Server) hasActiveRuntimeOwnersLocked() bool {
	return len(s.sessionRuns) != 0 || s.activeRuntimeOperations != 0
}
