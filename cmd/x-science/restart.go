package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"

	"x-science/internal/config"
)

func newRuntimeRestartRequester(requests chan<- string) func(string) error {
	var requested atomic.Bool
	return func(reason string) error {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return errors.New("runtime restart reason is required")
		}
		if !requested.CompareAndSwap(false, true) {
			return errors.New("a runtime restart is already pending")
		}
		select {
		case requests <- reason:
			return nil
		default:
			return errors.New("a runtime restart is already pending")
		}
	}
}

func runMain() error {
	if err := config.UpgradeProcessEnvironment(); err != nil {
		return err
	}
	return finishRuntimeRun(run(), launchRuntimeReplacement)
}

func launchRuntimeReplacement() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, os.Args[1:]...)
	command.Env = os.Environ()
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
