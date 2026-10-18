package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const sessionRuntimeCleanupTimeout = 20 * time.Second

// releaseSessionRunnerKernels is the terminal resource gate for a logical
// task. Persistent Python/R/REPL workers are useful between tool calls, but
// they must not outlive a terminal root task. Recoverable execution-unit
// interruptions deliberately retain them. Detached executors consume the same
// persisted task terminal state through their existing cancellation reconciler.
// One-shot
// software_runtime executors close at their own durable settlement boundary;
// this closes the remaining in-process session workers without touching other
// tasks.
func (s *Server) releaseSessionRunnerKernels(
	parent context.Context,
	sessionID string,
	authority *transcriptRunnerAuthority,
) error {
	if s == nil || s.kernelManager == nil {
		return nil
	}
	rootFrameID := strings.TrimSpace(sessionID)
	if authority != nil {
		if authority.Stream.FrameID != "" && authority.Stream.RootFrameID != "" && authority.Stream.FrameID != authority.Stream.RootFrameID {
			// A delegated runner is not the root task's lifetime owner. Even
			// its frame worker can host an independent manual cell. Its agent
			// executions settle through their exact execution owner; shared
			// workers remain under the root lifecycle and existing idle policy.
			return nil
		}
		rootFrameID = firstNonEmpty(strings.TrimSpace(authority.Stream.RootFrameID), rootFrameID)
	}
	if rootFrameID == "" {
		return errors.New("terminal runtime cleanup has no root frame authority")
	}
	if parent == nil {
		parent = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), sessionRuntimeCleanupTimeout)
	defer cancel()
	closed, err := s.kernelManager.CloseSession(cleanupCtx, rootFrameID)
	if err != nil {
		return fmt.Errorf("close %d task kernel(s) for %s: %w", closed, rootFrameID, err)
	}
	return nil
}
