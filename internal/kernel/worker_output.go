package kernel

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// workerOutput owns the parent read ends, independently of Cmd.Wait. Physical
// exit withdraws admission immediately; done is published only after the drain
// has either finished or been explicitly aborted within the post-exit budget.
type workerOutput struct {
	stdout, stderr                *os.File
	stdoutChild, stderrChild      *os.File
	protocolDone, diagnosticsDone chan struct{}
	exited, stop                  chan struct{}
	stopOnce                      sync.Once
	drainErr                      error // Written by the sole wait owner, before done.
}

func prepareWorkerOutput(command *exec.Cmd) (*workerOutput, error) {
	owner := &workerOutput{
		protocolDone: make(chan struct{}), diagnosticsDone: make(chan struct{}),
		exited: make(chan struct{}), stop: make(chan struct{}),
	}
	var err error
	owner.stdout, owner.stdoutChild, err = os.Pipe()
	if err == nil {
		owner.stderr, owner.stderrChild, err = os.Pipe()
	}
	if err != nil {
		owner.abortStart()
		return nil, fmt.Errorf("open kernel output: %w", err)
	}
	command.Stdout, command.Stderr = owner.stdoutChild, owner.stderrChild
	return owner, nil
}

func (owner *workerOutput) closeChildEnds() {
	for _, file := range []*os.File{owner.stdoutChild, owner.stderrChild} {
		if file != nil {
			_ = file.Close()
		}
	}
}

func (owner *workerOutput) closeReaders() {
	for _, file := range []*os.File{owner.stdout, owner.stderr} {
		if file != nil {
			_ = file.Close()
		}
	}
}

func (owner *workerOutput) abortStart() {
	owner.closeChildEnds()
	owner.closeReaders()
}

func (owner *workerOutput) start(worker *Worker) {
	go func() {
		defer close(owner.protocolDone)
		worker.readProtocol(owner.stdout)
	}()
	go func() {
		defer close(owner.diagnosticsDone)
		_, _ = io.Copy(worker.diagnostics, owner.stderr)
	}()
}

func (owner *workerOutput) afterExit() {
	defer owner.closeReaders()
	timer := time.NewTimer(workerProcessWaitDelay)
	defer timer.Stop()
	protocol, diagnostics := owner.protocolDone, owner.diagnosticsDone
	for protocol != nil || diagnostics != nil {
		select {
		case <-protocol:
			protocol = nil
		case <-diagnostics:
			diagnostics = nil
		case <-timer.C:
			if channelClosed(owner.protocolDone) && channelClosed(owner.diagnosticsDone) {
				return
			}
			owner.drainErr = errors.New("kernel output drain incomplete after process exit")
			owner.stopOnce.Do(func() { close(owner.stop) })
			// Closing our reads releases OS reads and stop releases protocol sends.
			// A synchronous service observer can still be inside its own callback:
			// never hide that limitation in an unbounded join or callback goroutine.
			return
		}
	}
}

func channelClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (w *Worker) physicalExit() <-chan struct{} {
	if w.output != nil {
		return w.output.exited
	}
	return w.done
}

func (w *Worker) hasExited() bool {
	select {
	case <-w.physicalExit():
		return true
	default:
		return false
	}
}

func (w *Worker) drainStop() <-chan struct{} {
	if w.output != nil {
		return w.output.stop
	}
	return w.done
}

func (w *Worker) closeInput() {
	w.closeOnce.Do(func() {
		if w.stdin != nil {
			_ = w.stdin.Close()
		}
	})
}

func (w *Worker) killWhileLive() {
	if w.process != nil && !w.hasExited() {
		_ = w.process.kill()
	}
}
