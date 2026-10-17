package kernel

import (
	"context"
	"os/exec"
)

// workerCommand identifies a one-shot operation without a process-global
// registry or a marker supplied by the target's environment. Callers may finish
// configuring the embedded command before startWorkerProcess freezes it.
type workerCommand struct {
	*exec.Cmd
	context  context.Context
	terminal workerCommandTerminal
	owned    *workerProcess
}

type workerCommandTerminal interface {
	wait() error
}

// Confined, long-lived workers still supply an ordinary *exec.Cmd. Only the
// private constructor above opts into the one-shot terminal boundary.
type workerProcessCommand interface {
	*exec.Cmd | *workerCommand
}

func unwrapWorkerCommand[C workerProcessCommand](command C) (*exec.Cmd, *workerCommand) {
	switch value := any(command).(type) {
	case *workerCommand:
		return value.Cmd, value
	case *exec.Cmd:
		return value, nil
	}
	panic("unreachable worker command type")
}

func waitWorkerCommand[C workerProcessCommand](command C) error {
	raw, oneShot := unwrapWorkerCommand(command)
	if oneShot != nil {
		return oneShot.Wait()
	}
	return raw.Wait()
}

func (command *workerCommand) Start() error {
	process, err := startWorkerProcess(command)
	if err == nil {
		command.owned = process
	}
	return err
}

func (command *workerCommand) Run() error {
	return runWorkerProcess(command)
}

func (command *workerCommand) Wait() error {
	if command.owned != nil {
		defer command.owned.close()
	}
	if command.terminal != nil {
		return command.terminal.wait()
	}
	return command.Cmd.Wait()
}
