package server

import (
	"errors"
	"sort"
	"strings"
)

// ErrRuntimeComponentStarting identifies initial preparation, not a failure.
// A previously failed component remains failed until it explicitly reports ready.
var ErrRuntimeComponentStarting = errors.New("runtime component starting")

type runtimeComponentState string

const (
	runtimeComponentStarting runtimeComponentState = "starting"
	runtimeComponentFailed   runtimeComponentState = "failed"
)

// ReportRuntimeComponent records only a bounded identity and lifecycle state.
// Error text is deliberately not retained or exposed by the health endpoint.
func (s *Server) ReportRuntimeComponent(name string, componentErr error) {
	if s == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return
	}
	s.runtimeComponentsMu.Lock()
	defer s.runtimeComponentsMu.Unlock()
	if componentErr == nil {
		delete(s.runtimeComponents, name)
		return
	}
	if s.runtimeComponents == nil {
		s.runtimeComponents = make(map[string]runtimeComponentState)
	}
	if errors.Is(componentErr, ErrRuntimeComponentStarting) {
		if s.runtimeComponents[name] != runtimeComponentFailed {
			s.runtimeComponents[name] = runtimeComponentStarting
		}
		return
	}
	s.runtimeComponents[name] = runtimeComponentFailed
}

// runtimeComponentHealth copies identities and states under the same lock so a
// concurrent ready/failure transition cannot produce contradictory health data.
func (s *Server) runtimeComponentHealth() ([]string, map[string]runtimeComponentState) {
	if s == nil {
		return nil, nil
	}
	s.runtimeComponentsMu.RLock()
	components := make([]string, 0, len(s.runtimeComponents))
	states := make(map[string]runtimeComponentState, len(s.runtimeComponents))
	for name, state := range s.runtimeComponents {
		components = append(components, name)
		states[name] = state
	}
	s.runtimeComponentsMu.RUnlock()
	sort.Strings(components)
	return components, states
}
