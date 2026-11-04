//go:build windows

package shellops

import (
	"os/exec"

	"x-science/internal/processsupervisor"
)

type sandboxProcess struct {
	job     *processsupervisor.Job
	startup *processsupervisor.ReservedStartupJob
}

func startReservedSandboxProcess(command *exec.Cmd) (*sandboxProcess, error) {
	reservation := &processsupervisor.ReservedStartupJob{}
	job, err := processsupervisor.StartWithReservedStartupJob(command, reservation)
	if err != nil {
		if reservation.CleanupPending() {
			return &sandboxProcess{startup: reservation}, err
		}
		return nil, err
	}
	return &sandboxProcess{job: job}, nil
}

func startSandboxProcess(command *exec.Cmd) (*sandboxProcess, error) {
	job, err := processsupervisor.Start(command)
	if err != nil {
		return nil, err
	}
	return &sandboxProcess{job: job}, nil
}

func (process *sandboxProcess) kill() error {
	if process == nil || process.job == nil {
		return nil
	}
	return process.job.Terminate()
}

func (process *sandboxProcess) close() error {
	if process.startup != nil {
		return process.startup.RetryCleanup()
	}
	return process.job.Close()
}
