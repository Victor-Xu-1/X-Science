//go:build linux

package kernel

import (
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type oneShotRequest struct {
	Path              string
	Args              []string
	Environment       []string
	ExtraFiles        int
	ExtraFilePresence []byte
}

// This is a per-command transport owner, not a scheduler or retry authority.
// Physical completion and observable failure have deliberately separate signals.
type oneShotProcess struct {
	command      *workerCommand
	helper       *exec.Cmd
	output       *oneShotIO
	request      oneShotRequest
	controlRead  *os.File
	controlWrite *os.File
	receiptRead  *os.File
	receiptWrite *os.File

	mu            sync.Mutex
	granted       bool
	stopping      bool
	completeKnown bool
	hasObserver   bool
	observation   error
	startupErr    error
	result        oneShotReceipt
	physicalErr   error
	notice        *time.Timer
	stopOnce      sync.Once
	closeOnce     sync.Once
	observeOnce   sync.Once
	completeOnce  sync.Once
	releaseOnce   sync.Once
	observed      chan struct{}
	complete      chan struct{}
	physicalDone  chan struct{}
	released      chan struct{}
}

func prepareOneShotProcess(command *workerCommand) (*oneShotProcess, error) {
	if command.Process != nil {
		return nil, errors.New("exec: already started")
	}
	if command.Err != nil {
		return nil, command.Err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate one-shot process helper: %w", err)
	}
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	receiptRead, receiptWrite, err := os.Pipe()
	if err != nil {
		_ = controlRead.Close()
		_ = controlWrite.Close()
		return nil, err
	}
	helper := exec.Command(executable, oneShotHelperArgument, strconv.Itoa(len(command.ExtraFiles)))
	helper.Dir, helper.Stdin = command.Dir, command.Stdin
	helper.Env = []string{}
	attributes := *command.SysProcAttr
	attributes.Pdeathsig = 0
	helper.SysProcAttr = &attributes
	// With no exec-owned context this only drains I/O after helper exit; it
	// cannot time out and kill a reaper that still owns running children.
	helper.WaitDelay = workerProcessWaitDelay
	helper.ExtraFiles = append(append([]*os.File(nil), command.ExtraFiles...), controlRead, receiptWrite)
	output, err := prepareOneShotIO(command.Cmd, helper)
	if err != nil {
		_ = controlRead.Close()
		_ = controlWrite.Close()
		_ = receiptRead.Close()
		_ = receiptWrite.Close()
		return nil, err
	}
	presence := make([]byte, len(command.ExtraFiles))
	for index, file := range command.ExtraFiles {
		if file != nil {
			presence[index] = 1
		}
	}
	process := &oneShotProcess{
		command: command, helper: helper, output: output,
		request: oneShotRequest{
			Path: command.Path, Args: append([]string(nil), command.Args...),
			Environment: command.Environ(), ExtraFiles: len(command.ExtraFiles),
			ExtraFilePresence: presence,
		},
		controlRead: controlRead, controlWrite: controlWrite,
		receiptRead: receiptRead, receiptWrite: receiptWrite,
		observed: make(chan struct{}), complete: make(chan struct{}),
		physicalDone: make(chan struct{}), released: make(chan struct{}),
	}
	command.terminal = process
	return process, nil
}

func (process *oneShotProcess) started(startErr error) {
	_ = process.controlRead.Close()
	_ = process.receiptWrite.Close()
	if startErr != nil {
		_ = process.controlWrite.Close()
		_ = process.receiptRead.Close()
		process.output.abortStart()
		return
	}
	// Background Wait changes only the private helper command, never the
	// caller's ProcessState after an unknown-outcome return.
	process.command.Process = process.helper.Process
	process.output.started()
	go func() {
		err := process.helper.Wait()
		process.mu.Lock()
		process.physicalErr = err
		process.mu.Unlock()
		close(process.physicalDone)
	}()
	go process.readReceipts()
	go func() {
		if err := gob.NewEncoder(process.controlWrite).Encode(process.request); err != nil {
			process.requestStop()
		}
	}()
	go func() {
		select {
		case <-process.command.context.Done():
			process.requestStop()
		case <-process.complete:
		case <-process.released:
		}
	}()
}

func (process *oneShotProcess) requestStop() {
	process.stopOnce.Do(func() {
		process.mu.Lock()
		process.stopping = true
		process.mu.Unlock()
		_ = process.controlWrite.Close()
		select {
		case <-process.complete:
			return
		default:
		}
		process.mu.Lock()
		process.notice = time.AfterFunc(5*time.Second, func() {
			process.mu.Lock()
			granted := process.granted
			process.mu.Unlock()
			if !granted {
				process.lostReceipt()
				return
			}
			process.observe("cleanup_incomplete", errors.New("one-shot helper has not confirmed cleanup"))
		})
		process.mu.Unlock()
	})
}

func (process *oneShotProcess) stop() error {
	process.requestStop()
	select {
	case <-process.complete:
		process.mu.Lock()
		defer process.mu.Unlock()
		return errors.Join(process.observation, process.result.failureError())
	case <-process.observed:
		process.mu.Lock()
		defer process.mu.Unlock()
		return process.observation
	}
}

func (process *oneShotProcess) observe(state string, cause error) {
	process.observeOnce.Do(func() {
		process.mu.Lock()
		if process.completeKnown {
			process.mu.Unlock()
			return
		}
		failure := &WorkerProcessCleanupError{State: state, Cause: cause}
		process.observation = failure
		process.mu.Unlock()
		owned := reportWorkerCleanup(process.command.context, failure)
		process.mu.Lock()
		process.hasObserver = owned
		process.mu.Unlock()
		// The result remains latched even if a later ECHILD receipt arrives.
		close(process.observed)
	})
}

func (process *oneShotProcess) finish(result oneShotReceipt) {
	process.completeOnce.Do(func() {
		process.mu.Lock()
		process.result, process.completeKnown = result, true
		if process.notice != nil {
			process.notice.Stop()
		}
		process.mu.Unlock()
		close(process.complete)
		process.requestStop()
	})
}

func (process *oneShotProcess) grant() error {
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.stopping || process.command.context.Err() != nil {
		return errors.New("one-shot startup was cancelled before grant")
	}
	count, err := process.controlWrite.Write([]byte{oneShotLaunchGrant})
	process.granted = count == 1
	return err
}

func (process *oneShotProcess) readReceipts() {
	defer process.receiptRead.Close()
	ready := false
	for {
		receipt, err := readOneShotReceipt(process.receiptRead)
		if err != nil {
			process.lostReceipt()
			return
		}
		switch receipt.kind {
		case oneShotReady:
			if ready {
				process.lostReceipt()
				return
			}
			ready = true
			if err := process.grant(); err != nil {
				process.lostReceipt()
				return
			}
		case oneShotIncomplete:
			process.mu.Lock()
			granted := process.granted
			process.mu.Unlock()
			if !granted {
				process.lostReceipt()
				return
			}
			process.observe("cleanup_incomplete", receipt.failureError())
		case oneShotComplete:
			process.mu.Lock()
			granted := process.granted
			process.mu.Unlock()
			if receipt.root && !granted {
				process.lostReceipt()
				return
			}
			process.finish(receipt)
			return
		}
	}
}

func (process *oneShotProcess) lostReceipt() {
	process.mu.Lock()
	granted := process.granted
	if !granted {
		// No launch grant was ever written. This exact helper can be killed
		// without attributing any target or descendants to a numeric PID.
		process.stopping = true
		process.startupErr = errors.New("one-shot startup protocol failed before target launch")
	}
	process.mu.Unlock()
	process.requestStop()
	if !granted {
		_ = process.helper.Process.Kill()
		process.finish(oneShotReceipt{kind: oneShotComplete, failure: oneShotSetupFailure})
		return
	}
	process.observe("cleanup_unknown", errors.New("one-shot terminal receipt is unavailable"))
}

func (process *oneShotProcess) wait() error {
	select {
	case <-process.complete:
		return process.completedWait()
	case <-process.observed:
		process.mu.Lock()
		owned, failure := process.hasObserver, process.observation
		process.mu.Unlock()
		if owned {
			// The operation observer has returned the typed error separately.
			// Keep its physical stack, staging and locks until real settlement.
			<-process.complete
			return process.completedWait()
		}
		process.requestStop()
		process.releaseOnce.Do(func() { close(process.released) })
		return errors.Join(failure, process.command.context.Err(), process.output.detach())
	}
}

func (process *oneShotProcess) completedWait() error {
	process.mu.Lock()
	result, startupErr := process.result, process.startupErr
	process.mu.Unlock()
	var waitErr, outputErr error
	if result.root {
		<-process.physicalDone
		process.command.ProcessState = process.helper.ProcessState
		process.mu.Lock()
		waitErr = process.physicalErr
		process.mu.Unlock()
		outputErr = process.output.afterExit()
		if !result.status.Exited() || result.status.ExitStatus() != 0 {
			waitErr = &oneShotTargetExitError{status: result.status, cause: waitErr}
		}
	} else {
		// Known pre-execution failure must not occupy an operation forever,
		// even if its ungranted helper cannot be reaped by this OS.
		outputErr = process.output.detach()
	}
	process.mu.Lock()
	observed := process.observation
	process.mu.Unlock()
	process.releaseOnce.Do(func() { close(process.released) })
	return errors.Join(waitErr, result.failureError(), startupErr, observed, process.command.context.Err(), outputErr)
}

func (process *oneShotProcess) close() {
	process.closeOnce.Do(func() {
		process.requestStop()
		select {
		case <-process.complete:
		case <-process.observed:
			process.mu.Lock()
			owned := process.hasObserver
			process.mu.Unlock()
			if owned {
				<-process.complete
			}
		}
		_ = process.output.detach()
		process.releaseOnce.Do(func() { close(process.released) })
	})
}

type oneShotTargetExitError struct {
	status syscall.WaitStatus
	cause  error
}

func (failure *oneShotTargetExitError) Error() string {
	if failure.status.Signaled() {
		return "signal: " + failure.status.Signal().String()
	}
	return "exit status " + strconv.Itoa(failure.status.ExitStatus())
}

func (failure *oneShotTargetExitError) Unwrap() error { return failure.cause }

func (receipt oneShotReceipt) failureError() error {
	if receipt.failure == 0 {
		return nil
	}
	stage := map[uint32]string{
		oneShotSetupFailure: "setup", oneShotStartFailure: "target start",
		oneShotCleanupFailure: "cleanup", oneShotWaitFailure: "target result",
	}[receipt.failure]
	if receipt.errno != 0 {
		return fmt.Errorf("one-shot process %s failed: %w", stage, receipt.errno)
	}
	return fmt.Errorf("one-shot process %s failed", stage)
}
