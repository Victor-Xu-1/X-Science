package host

import (
	"errors"
	"fmt"
	"os/exec"
	"time"

	"x-science/internal/subprocess"
)

// These startup helpers run under h.mu. Ownership starts immediately after
// cmd.Start, before Attach or any further fallible initialization. One reaper
// owns Wait and control.Close on both successful and failed startup paths.
func (h *Host) ownStartedProcess(id string, plugin Plugin, command *exec.Cmd, control *subprocess.Control) *externalProcess {
	process := &externalProcess{command: command, control: control, done: make(chan struct{})}
	h.processes[id] = process
	plugin.Runtime = RuntimeStatus{
		Kind: "external_process", Status: "starting", PID: command.Process.Pid,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	h.plugins[id] = plugin
	return process
}

func (h *Host) finishProcessStartup(id string, process *externalProcess, startErr error) error {
	process.startErr = startErr
	if startErr != nil {
		h.stopExternalProcess(id, process)
		h.stopping = true
	}
	plugin := h.plugins[id]
	if startErr != nil {
		plugin.Runtime.Status = "failed"
		plugin.Runtime.Error = errors.Join(startErr, process.stopErr).Error()
	} else {
		plugin.Runtime.Status = "running"
	}
	h.plugins[id] = plugin
	go h.waitForProcess(id, process)
	return startErr
}

func (h *Host) recordUnstartedProcessFailure(id string, plugin Plugin, control *subprocess.Control, startErr error) error {
	closeErr := control.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("plugin %s unstarted control cleanup: %w", id, closeErr)
		h.shutdownErr = errors.Join(h.shutdownErr, closeErr)
	}
	err := errors.Join(startErr, closeErr)
	plugin.Runtime = RuntimeStatus{Kind: "external_process", Status: "failed", Error: err.Error()}
	h.plugins[id] = plugin
	return err
}
