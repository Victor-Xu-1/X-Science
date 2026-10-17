//go:build linux

package kernel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

func newPlatformWorkerCommand(_ context.Context, executable string, arguments ...string) *exec.Cmd {
	// The one-shot owner, not os/exec's context watcher, requests lifetime EOF.
	// An exec-owned timer must never SIGKILL the reaper before settlement.
	return exec.Command(executable, arguments...)
}

func configureWorkerProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

type workerProcess struct {
	command    *exec.Cmd
	startTicks uint64
	oneShot    *oneShotProcess
}

func startWorkerProcess[C workerProcessCommand](input C, directories ...string) (*workerProcess, error) {
	command, oneShot := unwrapWorkerCommand(input)
	configureWorkerProcess(command)
	process := &workerProcess{command: command}
	var err error
	if oneShot != nil {
		if err := oneShot.context.Err(); err != nil {
			return nil, err
		}
		process.oneShot, err = prepareOneShotProcess(oneShot)
		if err != nil {
			return nil, err
		}
		command = process.oneShot.helper
		process.command = command
	}
	release, err := configureWorkerResourceDomain(command, directories)
	if err != nil {
		if process.oneShot != nil {
			process.oneShot.started(err)
		}
		return nil, err
	}
	defer release()
	publish := bindWorkerProcessCancellation(command, process.kill)
	defer publish()
	err = command.Start()
	if process.oneShot != nil {
		process.oneShot.started(err)
	}
	if err != nil {
		return nil, err
	}
	if process.oneShot != nil {
		return process, nil
	}
	if runtime.GOOS == "linux" {
		start, err := linuxProcessStartTicks(command.Process.Pid)
		if err != nil {
			publish()
			_ = command.Process.Kill()
			_ = command.Wait()
			return nil, fmt.Errorf("capture worker process identity: %w", err)
		}
		process.startTicks = uint64(start)
	}
	return process, nil
}

func runWorkerProcess[C workerProcessCommand](command C) error {
	process, err := startWorkerProcess(command)
	if err != nil {
		return err
	}
	defer process.close()
	return waitWorkerCommand(command)
}

func (process *workerProcess) signal(signal os.Signal) error {
	if process == nil || process.command == nil || process.command.Process == nil {
		return nil
	}
	if process.oneShot != nil {
		return process.oneShot.stop()
	}
	value, ok := signal.(syscall.Signal)
	if !ok {
		return process.command.Process.Signal(signal)
	}
	if runtime.GOOS == "linux" {
		return process.signalLinuxTree(value)
	}
	if err := syscall.Kill(-process.command.Process.Pid, value); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

func (process *workerProcess) kill() error {
	return process.signal(syscall.SIGKILL)
}

// killWorkerProcess keeps Linux confinement probes on the same process-group
// termination primitive without constructing a long-lived Worker.
func killWorkerProcess(command *exec.Cmd) error {
	return (&workerProcess{command: command}).kill()
}

func (process *workerProcess) close() {
	if process != nil && process.oneShot != nil {
		process.oneShot.close()
	}
}

func unexpectedKernelStopReason(command *exec.Cmd, waitErr error) string {
	if command != nil && command.ProcessState != nil {
		if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				if status.Signal() == syscall.SIGKILL {
					return "was killed — possibly out of memory"
				}
				return "was killed by " + unixKernelSignalName(status.Signal())
			}
			if code := status.ExitStatus(); code == 0 {
				return "exited unexpectedly (exit code 0)"
			} else if code == 128+int(syscall.SIGKILL) {
				// Bubblewrap and other process supervisors may translate the
				// worker's SIGKILL into the conventional shell exit code 137.
				// Preserve the same resource diagnostic as a directly signalled
				// process so settlement can recover with a smaller working set.
				return "was killed — possibly out of memory"
			} else if code > 0 {
				return fmt.Sprintf("exited unexpectedly with exit code %d", code)
			}
		}
	}
	if waitErr != nil {
		return "exited unexpectedly"
	}
	return "exited unexpectedly (exit code 0)"
}

func unixKernelSignalName(signal syscall.Signal) string {
	names := map[syscall.Signal]string{
		syscall.SIGHUP: "SIGHUP", syscall.SIGINT: "SIGINT", syscall.SIGQUIT: "SIGQUIT",
		syscall.SIGILL: "SIGILL", syscall.SIGTRAP: "SIGTRAP", syscall.SIGABRT: "SIGABRT",
		syscall.SIGBUS: "SIGBUS", syscall.SIGFPE: "SIGFPE", syscall.SIGKILL: "SIGKILL",
		syscall.SIGUSR1: "SIGUSR1", syscall.SIGSEGV: "SIGSEGV", syscall.SIGUSR2: "SIGUSR2",
		syscall.SIGPIPE: "SIGPIPE", syscall.SIGALRM: "SIGALRM", syscall.SIGTERM: "SIGTERM",
	}
	if name := names[signal]; name != "" {
		return name
	}
	return fmt.Sprintf("signal %d", signal)
}
