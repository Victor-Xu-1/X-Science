//go:build linux

package kernel

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestManagedExecutionPreservesProducedTerminalAcrossWorkerExit(t *testing.T) {
	const executionID = "protocol-exit-drain-execution"
	command := exec.Command("/bin/sh", "-c", `
IFS= read -r request || exit 1
printf '%s\n' '{"type":"execution_started","id":"protocol-exit-drain-execution"}'
printf '%s\n' '{"id":"protocol-exit-drain-execution","stdout":"final-stdout-marker\n","stderr":"final-stderr-marker\n","trace":{"terminal_marker":true}}'
printf '%s\n' 'owned-worker-diagnostic' >&2
`)
	command.Env = []string{}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := prepareWorkerOutput(command)
	if err != nil {
		_ = stdin.Close()
		t.Fatal(err)
	}
	stdout, stderr := output.stdout, output.stderr
	process, err := startWorkerProcess(command)
	if err != nil {
		_ = stdin.Close()
		output.abortStart()
		t.Fatal(err)
	}
	output.closeChildEnds()
	manager := NewManager(Config{ShutdownTimeout: time.Second})
	worker := &Worker{
		id: "protocol-exit-drain-worker", workspaceDir: t.TempDir(), manager: manager,
		command: command, process: process, stdin: stdin, responses: make(chan Response, 1),
		output: output,
		done:   make(chan struct{}), diagnostics: newTailBuffer(4096),
		startWait: make(map[string]chan struct{}),
	}
	manager.workers[worker.id] = worker
	reader := &terminalDeliveryGate{
		source: bufio.NewReader(stdout), captured: make(chan []byte, 1), release: make(chan struct{}),
	}
	protocolDone, diagnosticDone, waited := output.protocolDone, output.diagnosticsDone, make(chan struct{})
	readersStarted := false
	var waitOnce sync.Once
	startWait := func() { waitOnce.Do(func() { go func() { worker.wait(); close(waited) }() }) }
	t.Cleanup(func() {
		reader.unblock()
		_ = stdin.Close()
		startWait() // Exactly one real owner invokes Cmd.Wait and publishes worker.done.
		select {
		case <-waited:
		case <-time.After(3 * time.Second):
			_ = process.kill() // This test-owned, start-ticks-bound worker only.
			select {
			case <-waited:
			case <-time.After(3 * time.Second):
				t.Error("owned worker did not settle during bounded cleanup")
			}
		}
		_ = stdout.Close()
		_ = stderr.Close()
		if readersStarted {
			for _, drained := range []<-chan struct{}{protocolDone, diagnosticDone} {
				select {
				case <-drained:
				case <-time.After(3 * time.Second):
					t.Error("owned pipe reader did not settle during bounded cleanup")
				}
			}
		}
	})
	pidfd, err := unix.PidfdOpen(command.Process.Pid, 0)
	if err != nil {
		t.Fatalf("open positive identity handle for test-owned worker: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(pidfd) })
	go func() { defer close(protocolDone); worker.readProtocol(reader) }()
	go func() { defer close(diagnosticDone); _, _ = io.Copy(worker.diagnostics, stderr) }()
	readersStarted = true
	execution := &managedExecution{
		worker: worker, state: &workerLifecycle{generation: 1, spec: SessionSpec{KernelID: worker.id}}, generation: 1,
		request: SubmitRequest{ExecID: executionID, FrameID: "protocol-exit-drain-frame", Language: "python", Origin: "agent"},
		key:     executionKey{manager: "protocol-exit-drain-fixture", frameID: "protocol-exit-drain-frame", execID: executionID},
		status:  "queued", interrupt: make(chan struct{}, 1), hostCancel: make(chan struct{}),
		started: make(chan ExecutionStarted, 1), done: make(chan ExecutionOutcome, 1), finished: make(chan struct{}),
	}
	go execution.run()
	var emitted Response
	select {
	case line := <-reader.captured:
		if err := json.Unmarshal(line, &emitted); err != nil || emitted.ID != executionID || emitted.Stdout == "" || emitted.Stderr == "" {
			t.Fatalf("fixture did not receive a complete real terminal frame: response=%#v err=%v", emitted, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owned worker did not emit its terminal frame")
	}
	select {
	case started := <-execution.started:
		if started.ExecID != executionID {
			t.Fatalf("execution did not reach its real started boundary: %#v", started)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("execution-start protocol frame was not routed")
	}
	// The identity-bound pidfd observes physical exit without reaping it or
	// closing worker.done. Only worker.wait below owns that terminal publication.
	fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	if n, err := unix.Poll(fds, 3000); err != nil || n != 1 || fds[0].Revents&unix.POLLIN == 0 {
		t.Fatalf("test-owned worker did not physically exit: ready=%d revents=%d err=%v", n, fds[0].Revents, err)
	}
	startWait()
	select {
	case <-output.exited:
	case <-time.After(3 * time.Second):
		t.Fatal("real wait owner did not record the physical exit")
	}
	if manager.workerByID(worker.id) != nil {
		t.Fatal("physically exited worker remained reusable during output drain")
	}
	var outcome ExecutionOutcome
	premature := false
	// This deadline is only an anti-hang release for an implementation that
	// correctly retains the terminal boundary until protocol drain. The baseline
	// failure is selected by real worker exit while the final frame is gated,
	// not by a sleep, repeated race attempt, or elapsed-time production policy.
	guard, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	select {
	case outcome = <-execution.done:
		premature = true
	case <-guard.Done():
	}
	reader.unblock()
	if !premature {
		select {
		case outcome = <-execution.done:
		case <-time.After(3 * time.Second):
			t.Fatal("managed execution did not settle after final-frame delivery")
		}
	}
	for _, drained := range []<-chan struct{}{protocolDone, diagnosticDone, waited} {
		select {
		case <-drained:
		case <-time.After(3 * time.Second):
			t.Fatal("owned protocol/diagnostic/reap goroutine did not drain")
		}
	}
	if premature || outcome.Err != nil || outcome.Response.ID != emitted.ID || outcome.Response.Stdout != emitted.Stdout || outcome.Response.Stderr != emitted.Stderr || outcome.Response.Trace["terminal_marker"] != true {
		t.Fatalf("produced terminal frame lost across physical worker exit: premature=%t response=%#v err=%v; emitted=%#v; diagnostics=%q",
			premature, outcome.Response, outcome.Err, emitted, strings.TrimSpace(worker.diagnostics.String()))
	}
}

// terminalDeliveryGate stops only delivery of the second complete frame. Bytes
// come from the actual owned process pipe; the fixture does not synthesize a
// terminal response, close worker.done, or replace the real wait/reap method.
type terminalDeliveryGate struct {
	source     *bufio.Reader
	captured   chan []byte
	release    chan struct{}
	once       sync.Once
	lines      int
	pending    []byte
	pendingErr error
}

func (reader *terminalDeliveryGate) unblock() { reader.once.Do(func() { close(reader.release) }) }

func (reader *terminalDeliveryGate) Read(buffer []byte) (int, error) {
	if len(reader.pending) == 0 {
		if reader.pendingErr != nil {
			return 0, reader.pendingErr
		}
		line, err := reader.source.ReadBytes('\n')
		if len(line) == 0 {
			return 0, err
		}
		reader.lines++
		reader.pending, reader.pendingErr = line, err
		if reader.lines == 2 {
			reader.captured <- append([]byte(nil), line...)
			<-reader.release
		}
	}
	n := copy(buffer, reader.pending)
	reader.pending = reader.pending[n:]
	return n, nil
}
