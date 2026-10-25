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
		once.Do(s.endRuntimeOperation)
	}, nil
}

// A canonical Stop has already committed. Its bounded signal/subscription
// and completed-proof handoff continue that accepted work during idle checks;
// the final original drain recheck observes this operation. Actual draining
// still refuses it. New HTTP/business admission keeps its stricter contract.
func (s *Server) beginAcceptedStopContinuation() (func(), error) {
	s.sessionRunsMu.Lock()
	if s.sessionRunsDraining {
		s.sessionRunsMu.Unlock()
		return nil, ErrRuntimeDraining
	}
	s.activeRuntimeOperations++
	s.sessionRunsMu.Unlock()
	var once sync.Once
	return func() { once.Do(s.endRuntimeOperation) }, nil
}

// Releasing an already-owned cleanup/operation never admits new work.
func (s *Server) endRuntimeOperation() {
	s.sessionRunsMu.Lock()
	s.activeRuntimeOperations--
	s.sessionRunsMu.Unlock()
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
	if len(s.sessionRuns) != 0 || s.activeRuntimeOperations != 0 {
		return true
	}
	// The existing supervisor registration also owns queued/pre-claim work.
	// Production insertion/removal shares this mutex with idle admission.
	active := false
	kernelChildRuns.Range(func(key, value any) bool {
		if key.(kernelChildRunKey).server != s {
			return true
		}
		select {
		case <-value.(*kernelChildRun).done:
			// A physically completed registration is not active ownership.
			return true
		default:
			active = true
			return false
		}
	})
	return active
}
