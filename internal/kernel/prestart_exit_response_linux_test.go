//go:build linux

package kernel

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestManagedPrestartExitResponsePreservesStopAndAcknowledgement(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		for _, stopped := range []bool{false, true} {
			t.Run(fmt.Sprintf("ack_%t/stop_%t", acknowledged, stopped), func(t *testing.T) {
				const executionID = "prestart-exit-cell"
				requestWritten, releaseWrite := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblockWrite := func() { releaseOnce.Do(func() { close(releaseWrite) }) }
				defer unblockWrite()
				var heldDiagnostic *os.File
				body := `printf '%s\n' '{"id":"prestart-exit-cell","stdout":"retained-terminal","preflight":{"execute":false}}'`
				if acknowledged {
					body = `printf '%s\n' '{"type":"execution_started","id":"prestart-exit-cell"}' '{"id":"prestart-exit-cell","stdout":"retained-terminal"}'`
				}
				worker := newOutputTestWorker(t, body, func(worker *Worker, _ *exec.Cmd) {
					// The unbuffered real protocol send proves the real run loop has
					// received the response once protocolDone closes. With no ack and
					// done still held, only the pre-start response case can consume it.
					worker.responses = make(chan Response)
					worker.stdin = &prestartRequestWriteGate{WriteCloser: worker.stdin, written: requestWritten, release: releaseWrite}
					duplicate, err := unix.Dup(int(worker.output.stderrChild.Fd()))
					if err != nil {
						t.Fatal(err)
					}
					unix.CloseOnExec(duplicate)
					heldDiagnostic = os.NewFile(uintptr(duplicate), "owned-prestart-diagnostic-eof")
					t.Cleanup(func() { _ = heldDiagnostic.Close() })
				})
				defer heldDiagnostic.Close()
				execution := newPrestartTestExecution(t, worker, executionID)
				go execution.run()
				waitOutputTestChannel(t, requestWritten, "real pre-start request write")
				waitOutputTestChannel(t, worker.physicalExit(), "real worker exit before response selection")
				if channelClosed(worker.done) {
					t.Fatal("fixture did not retain the output disposition boundary")
				}
				if stopped {
					result := worker.manager.InterruptSessionWithReason(execution.request.FrameID, "", "", executionID, "explicit-prestart-stop")
					if !result.Interrupted || result.Via != "sigint" {
						t.Fatalf("real user-stop admission failed: %#v", result)
					}
				}
				unblockWrite()
				waitOutputTestChannel(t, worker.output.protocolDone, "actual run loop response consumption")
				_ = heldDiagnostic.Close()
				var outcome ExecutionOutcome
				select {
				case outcome = <-execution.done:
				case <-time.After(4 * time.Second):
					t.Fatal("managed execution did not finish after diagnostic EOF release")
				}
				if outcome.Response.ID != executionID || outcome.Response.Stdout != "retained-terminal" || worker.output.drainErr != nil {
					t.Fatalf("matching terminal was not preserved: %#v drain=%v", outcome, worker.output.drainErr)
				}
				if stopped != (outcome.Err != nil) || stopped && !strings.Contains(outcome.Err.Error(), "explicit-prestart-stop") {
					t.Fatalf("response branch changed explicit stop authority: stopped=%t outcome=%#v", stopped, outcome)
				}
				started, hasStart := <-execution.started
				if acknowledged {
					if !hasStart || started.ExecID != executionID || outcome.StartedAt.IsZero() {
						t.Fatalf("acknowledged execution lost its start: start=%#v hasStart=%t outcome=%#v", started, hasStart, outcome)
					}
				} else if hasStart || !outcome.StartedAt.IsZero() || outcome.Response.Preflight["execute"] != false {
					t.Fatalf("preflight response invented an execution start: start=%#v hasStart=%t outcome=%#v", started, hasStart, outcome)
				}
			})
		}
	}
}

func newPrestartTestExecution(t *testing.T, worker *Worker, executionID string) *managedExecution {
	t.Helper()
	frameID := "prestart-exit-frame"
	execution := &managedExecution{
		worker: worker, state: &workerLifecycle{generation: 1, spec: SessionSpec{KernelID: worker.id, FrameID: frameID}}, generation: 1,
		request: SubmitRequest{ExecID: executionID, FrameID: frameID, Language: "python", Origin: "agent", InterruptGrace: 5 * time.Second},
		key:     executionKey{manager: fmt.Sprintf("%p", worker.manager), frameID: frameID, execID: executionID},
		status:  "queued", interrupt: make(chan struct{}, 1), hostCancel: make(chan struct{}),
		started: make(chan ExecutionStarted, 1), done: make(chan ExecutionOutcome, 1), finished: make(chan struct{}),
	}
	lifecycleRegistry.mu.Lock()
	lifecycleRegistry.executions[execution.key] = execution
	lifecycleRegistry.mu.Unlock()
	t.Cleanup(func() {
		lifecycleRegistry.mu.Lock()
		if lifecycleRegistry.executions[execution.key] == execution {
			delete(lifecycleRegistry.executions, execution.key)
		}
		lifecycleRegistry.mu.Unlock()
	})
	return execution
}

type prestartRequestWriteGate struct {
	io.WriteCloser
	written chan struct{}
	release <-chan struct{}
}

func (writer *prestartRequestWriteGate) Write(payload []byte) (int, error) {
	n, err := writer.WriteCloser.Write(payload)
	close(writer.written)
	<-writer.release
	return n, err
}
