//go:build linux

package kernel

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWorkerOutputExitContracts(t *testing.T) {
	for _, scenario := range []string{"terminal", "no_result", "unrelated_id"} {
		t.Run(scenario, func(t *testing.T) {
			body := `id=${request#*\"id\":\"}; id=${id%%\"*}
printf '{"id":"%s","stdout":"final-out","stderr":"final-err"}\n' "$id"
printf '%s\n' 'outer-diagnostic' >&2`
			if scenario == "no_result" {
				body = "exit 0"
			} else if scenario == "unrelated_id" {
				body = `printf '%s\n' '{"id":"another-execution","stdout":"not-current"}'`
			}
			worker := newOutputTestWorker(t, body, nil)
			response, err := worker.Execute(context.Background(), "fixture", "agent")
			waitOutputTestChannel(t, worker.done, "physical exit and output disposition")
			if scenario != "terminal" {
				if err == nil || response.ID != "" {
					t.Fatalf("missing exact terminal became success: response=%#v err=%v", response, err)
				}
				return
			}
			if err != nil || response.ID == "" || response.Stdout != "final-out" || response.Stderr != "final-err" || worker.output.drainErr != nil {
				t.Fatalf("lost drained terminal: response=%#v err=%v drain=%v", response, err, worker.output.drainErr)
			}
			if !strings.Contains(worker.diagnostics.String(), "outer-diagnostic") {
				t.Fatalf("final diagnostic was not drained: %q", worker.diagnostics.String())
			}
		})
	}
}

func TestWorkerOutputDrainReleasesControlWaits(t *testing.T) {
	t.Run("full_responses", func(t *testing.T) {
		worker := newOutputTestWorker(t, `printf '%s\n' '{"id":"first","stdout":"retained"}' '{"id":"second"}'`, func(worker *Worker, _ *exec.Cmd) {
			worker.responses = make(chan Response, 1)
		})
		if err := worker.writeProtocol([]byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		waitOutputTestChannel(t, worker.done, "full response send disposition")
		waitOutputTestChannel(t, worker.output.protocolDone, "aborted protocol sender")
		response, err := worker.responseAfterExit("first")
		if response.Stdout != "retained" || err == nil || worker.output.drainErr == nil {
			t.Fatalf("incomplete drain became success or lost buffered bytes: response=%#v err=%v", response, err)
		}
	})
	t.Run("full_host_calls", func(t *testing.T) {
		calls := make(chan HostCall, 1)
		worker := newOutputTestWorker(t, `
printf '%s\n' '{"type":"host_call","id":"hc-11111111111111111111111111111111","cell_id":"fixture","method":"host.fixture","args":[],"kwargs":{}}'
IFS= read -r ack || exit 2
printf '%s\n' '{"type":"host_call","id":"hc-22222222222222222222222222222222","cell_id":"fixture","method":"host.fixture","args":[],"kwargs":{}}'
IFS= read -r ack || exit 2`, func(worker *Worker, _ *exec.Cmd) {
			if err := worker.bindHostCalls("fixture", calls); err != nil {
				t.Fatal(err)
			}
		})
		if err := worker.writeProtocol([]byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		waitOutputTestChannel(t, worker.done, "full host-call send disposition")
		waitOutputTestChannel(t, worker.output.protocolDone, "host-call reader")
		if worker.output.drainErr != nil || len(calls) != 1 {
			t.Fatalf("host-call send retained a done/drain cycle: drain=%v queued=%d", worker.output.drainErr, len(calls))
		}
		if err := worker.writeHostAck("hc-33333333333333333333333333333333"); err == nil {
			t.Fatal("physically exited worker accepted a new host acknowledgement")
		}
	})
	t.Run("inherited_output_descriptor", func(t *testing.T) {
		var held *os.File
		worker := newOutputTestWorker(t, `printf '%s\n' '{"id":"fixture","stdout":"retained"}'`, func(worker *Worker, _ *exec.Cmd) {
			duplicate, err := unix.Dup(int(worker.output.stdoutChild.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			unix.CloseOnExec(duplicate)
			held = os.NewFile(uintptr(duplicate), "owned-inherited-output")
			t.Cleanup(func() { _ = held.Close() })
		})
		if err := worker.writeProtocol([]byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		waitOutputTestChannel(t, worker.done, "inherited output EOF budget")
		waitOutputTestChannel(t, worker.output.protocolDone, "closed owned output read")
		response, err := worker.responseAfterExit("fixture")
		if response.Stdout != "retained" || err == nil || worker.output.drainErr == nil {
			t.Fatalf("held EOF was accepted as fully drained: response=%#v err=%v", response, err)
		}
	})
}

func TestWorkerOutputExitClosesBlockedStdinWriter(t *testing.T) {
	gateRead, gateWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer gateWrite.Close()
	entered := make(chan struct{})
	var heldInput *os.File
	worker := newOutputTestWorker(t, `IFS= read -r release <&3 || :`, func(worker *Worker, command *exec.Cmd) {
		command.ExtraFiles = []*os.File{gateRead}
		duplicate, err := unix.Dup(int(command.Stdin.(*os.File).Fd()))
		if err != nil {
			t.Fatal(err)
		}
		unix.CloseOnExec(duplicate)
		heldInput = os.NewFile(uintptr(duplicate), "owned-inherited-input")
		t.Cleanup(func() { _ = heldInput.Close() })
		worker.stdin = &outputTestProgressWriter{WriteCloser: worker.stdin, entered: entered}
	})
	if err := worker.writeProtocol([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() { written <- worker.writeProtocol([]byte(strings.Repeat("x", 1<<20))) }()
	waitOutputTestChannel(t, entered, "positive stdin write before exit")
	_ = gateWrite.Close()
	waitOutputTestChannel(t, worker.done, "exit with an inherited stdin read end")
	select {
	case err := <-written:
		if err == nil {
			t.Fatal("blocked writer unexpectedly completed its entire payload")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("physical exit did not release the original stdin writer")
	}
}

func TestWorkerOutputObserverAbortIsExplicit(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	worker := newOutputTestWorker(t, `printf '%s\n' '{"type":"stdout_chunk","data":"live"}' '{"id":"fixture","stdout":"final"}'`, func(worker *Worker, _ *exec.Cmd) {
		if err := worker.bindStdoutStream("fixture", func(string) { close(entered); <-release }); err != nil {
			t.Fatal(err)
		}
	})
	if err := worker.writeProtocol([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitOutputTestChannel(t, entered, "synchronous observer entry")
	waitOutputTestChannel(t, worker.done, "explicit observer-blocked drain disposition")
	if worker.output.drainErr == nil || channelClosed(worker.output.protocolDone) {
		t.Fatal("blocked observer was silently treated as joined")
	}
	unblock()
	waitOutputTestChannel(t, worker.output.protocolDone, "observer returns to aborted parser")
}

func TestWorkerExitRetainedResponsePreservesStopAndRestartNotice(t *testing.T) {
	worker := newOutputTestWorker(t, "exit 0", nil)
	if err := worker.writeProtocol([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitOutputTestChannel(t, worker.done, "real exit before retained response consumer")
	terminal := Response{ID: "fixture", Stdout: "retained", Interrupted: true}
	execution := &managedExecution{worker: worker, request: SubmitRequest{ExecID: "fixture"}}
	execution.setUserStopReason("explicit-stop")
	outcome := execution.workerExitOutcome(time.Now(), &terminal)
	if outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "explicit-stop") || !outcome.Response.Interrupted || outcome.Response.Stdout != "retained" {
		t.Fatalf("exit priority erased stop authority: %#v", outcome)
	}
	execution.stopReason, execution.timedOut = "", true
	outcome = execution.workerExitOutcome(time.Now(), &terminal)
	if !errors.Is(outcome.Err, context.DeadlineExceeded) || !outcome.TimedOut {
		t.Fatalf("exit priority erased timeout authority: %#v", outcome)
	}
	state := &workerLifecycle{generation: 7, pendingRestart: "restart-notice", noticeGeneration: 7}
	lifecycleRegistry.mu.Lock()
	lifecycleRegistry.workers[worker] = state
	lifecycleRegistry.mu.Unlock()
	t.Cleanup(func() {
		lifecycleRegistry.mu.Lock()
		delete(lifecycleRegistry.workers, worker)
		lifecycleRegistry.mu.Unlock()
	})
	response := worker.withRestartNotice(terminal)
	if !strings.Contains(response.Stderr, "restart-notice") || strings.Contains(worker.withRestartNotice(terminal).Stderr, "restart-notice") {
		t.Fatal("direct Execute restart notice is missing or consumed twice")
	}
}

// This fixture owns its worker, descriptors and unique wait owner. It uses the
// production output owner without confinement/real scientific environments.
func newOutputTestWorker(t *testing.T, body string, configure func(*Worker, *exec.Cmd)) *Worker {
	t.Helper()
	command := exec.Command("/bin/sh", "-c", "IFS= read -r request || exit 1\n"+body)
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
	t.Cleanup(func() { _ = stdin.Close(); output.abortStart() })
	worker := &Worker{id: "output-fixture", workspaceDir: t.TempDir(), manager: NewManager(Config{}),
		command: command, stdin: stdin, output: output, responses: make(chan Response, 8),
		done: make(chan struct{}), diagnostics: newTailBuffer(4096), startWait: make(map[string]chan struct{})}
	if configure != nil {
		configure(worker, command)
	}
	process, err := startWorkerProcess(command)
	closeKernelCommandExtraFiles(command)
	if err != nil {
		_ = stdin.Close()
		output.abortStart()
		t.Fatal(err)
	}
	worker.process = process
	output.closeChildEnds()
	worker.manager.workers[worker.id] = worker
	t.Cleanup(func() {
		worker.closeInput()
		select {
		case <-worker.done:
		case <-time.After(3 * time.Second):
			worker.killWhileLive()
			waitOutputTestChannel(t, worker.done, "owned fixture cleanup")
		}
		output.closeReaders()
		waitOutputTestChannel(t, output.protocolDone, "owned protocol cleanup")
		waitOutputTestChannel(t, output.diagnosticsDone, "owned diagnostics cleanup")
	})
	output.start(worker)
	go worker.wait()
	return worker
}

func waitOutputTestChannel(t *testing.T, done <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

type outputTestProgressWriter struct {
	io.WriteCloser
	entered chan struct{}
}

func (writer *outputTestProgressWriter) Write(value []byte) (int, error) {
	if len(value) < 64 {
		return writer.WriteCloser.Write(value)
	}
	n, err := writer.WriteCloser.Write(value[:1])
	close(writer.entered)
	if err != nil {
		return n, err
	}
	rest, err := writer.WriteCloser.Write(value[1:])
	return n + rest, err
}
