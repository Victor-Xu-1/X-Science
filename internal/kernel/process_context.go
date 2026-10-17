package kernel

import (
	"context"
	"os/exec"
	"sync"
	"time"
)

// workerProcessWaitDelay bounds I/O draining after process exit. Linux one-shot
// helpers have no exec-owned context watcher, so this never limits the lifetime
// of a reaper that still owns children. Other platforms retain their existing
// context-cancellation process boundary.
const workerProcessWaitDelay = 2 * time.Second

// newWorkerProcessCommand makes context cancellation use the same process-tree
// termination authority as explicit worker shutdown. exec.CommandContext's
// default Cancel only kills the direct child, which can leave a micromamba,
// shell, or helper descendant holding stdout/stderr open and make Wait block.
func newWorkerProcessCommand(ctx context.Context, executable string, arguments ...string) *workerCommand {
	if ctx == nil {
		ctx = context.Background()
	}
	return &workerCommand{Cmd: newPlatformWorkerCommand(ctx, executable, arguments...), context: ctx}
}

// Bind before Start launches exec.Cmd's context watcher. The watcher must not
// read a half-published process identity, and Cancel must never be rewritten
// after Start. Non-context commands must retain a nil Cancel to remain valid.
func bindWorkerProcessCancellation(command *exec.Cmd, cancel func() error) func() {
	ready := make(chan struct{})
	var once sync.Once
	if command.Cancel != nil {
		command.Cancel = func() error { <-ready; return cancel() }
	}
	return func() { once.Do(func() { close(ready) }) }
}
