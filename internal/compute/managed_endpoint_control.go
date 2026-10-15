package compute

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"x-science/internal/subprocess"
)

// Only finite, trusted runtime control commands use this helper. Approved
// service scripts never use its exec-managed pipes or finite drain cutoff.
func managedEndpointControlCommand(ctx context.Context, executable string, arguments ...string) (string, error) {
	controlCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(controlCtx, executable, arguments...)
	command.Env = managedEndpointBaseEnvironment()
	command.WaitDelay = 200 * time.Millisecond
	control, err := subprocess.Prepare(command)
	if err != nil {
		return "", err
	}
	defer control.Close()
	var output, diagnostic cappedBuffer
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := command.Start(); err != nil {
		return "", err
	}
	if err := control.Attach(command); err != nil {
		_ = control.Kill(command)
		_ = command.Wait()
		return "", err
	}
	err = command.Wait()
	if controlCtx.Err() != nil {
		return output.String(), errors.Join(context.Cause(controlCtx), err)
	}
	return output.String(), err
}
