package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// StopExternalProcesses requests termination of every owned process before
// joining any of them. A timeout leaves ownership registered and prevents a
// replacement start until the original Wait/control cleanup actually completes.
func (h *Host) StopExternalProcesses(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("plugin shutdown context is required")
	}
	var errs []error
	h.mu.Lock()
	processes := make(map[string]*externalProcess, len(h.processes))
	for id, process := range h.processes {
		processes[id] = process
		if process.stopping {
			errs = append(errs, process.stopErr)
			continue
		}
		process.stopping = true
		plugin := h.plugins[id]
		plugin.Runtime.Status = "stopping"
		h.plugins[id] = plugin
		if err := process.control.Kill(process.command); err != nil && !errors.Is(err, os.ErrProcessDone) {
			process.stopErr = fmt.Errorf("terminate plugin %s: %w", id, err)
		}
		errs = append(errs, process.stopErr)
	}
	h.stopping = len(h.processes) != 0
	h.mu.Unlock()

	for id, process := range processes {
		// Prefer actual completed cleanup even if the caller's deadline expires
		// at the same instant. Repeated callers join the same generation.
		select {
		case <-process.done:
		default:
			select {
			case <-process.done:
			case <-ctx.Done():
				return errors.Join(errors.Join(errs...), fmt.Errorf("join plugin %s: %w", id, ctx.Err()))
			}
		}
		errs = append(errs, process.waitErr)
	}
	return errors.Join(errs...)
}

func (h *Host) waitForProcess(id string, process *externalProcess) {
	waitErr := process.command.Wait()
	h.mu.Lock()
	// Serialize control.Close with Stop's Kill, including Windows Job handles.
	closeErr := process.control.Close()
	process.waitErr = closeErr
	var exitErr *exec.ExitError
	if waitErr != nil && (!process.stopping || !errors.As(waitErr, &exitErr)) {
		process.waitErr = errors.Join(waitErr, closeErr)
	}
	if h.processes[id] == process {
		delete(h.processes, id)
		plugin := h.plugins[id]
		plugin.Runtime.PID = 0
		plugin.Runtime.StartedAt = ""
		plugin.Runtime.Error = ""
		switch {
		case process.stopping:
			plugin.Runtime.Status = "stopped"
			if err := errors.Join(process.stopErr, process.waitErr); err != nil {
				plugin.Runtime.Status = "failed"
				plugin.Runtime.Error = err.Error()
			}
		case process.waitErr != nil:
			plugin.Runtime.Status = "failed"
			plugin.Runtime.Error = process.waitErr.Error()
		default:
			plugin.Runtime.Status = "exited"
		}
		h.plugins[id] = plugin
	}
	if len(h.processes) == 0 {
		h.stopping = false
	}
	h.mu.Unlock()
	close(process.done)
}
