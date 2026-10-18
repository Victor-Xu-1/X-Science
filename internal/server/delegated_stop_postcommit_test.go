package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestDelegatedStopCommittedSignalSurvivesCallerDisconnect(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "postcommit-disconnect",
		Requests: []workspace.KernelDelegateRequest{{Task: "Exact target"}, {Task: "Unaffected sibling"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	childCtx, _, finishChild := delegatedActiveRun(t, f, children[0], "committed-target")
	siblingCtx, siblingClaim, _ := delegatedActiveRun(t, f, children[1], "untouched-sibling")
	request, _ := delegatedAPIRequest(t, f, children[0].FrameID)
	caller, disconnect := context.WithCancel(t.Context())
	receipt, err := f.store.AdmitDelegatedStop(caller, f.stream.OwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	// The durable transaction has committed. Disconnecting the initiating
	// transport must not suppress the exact signal already authorized by it.
	disconnect()
	drained, err := f.server.signalDelegatedStop(caller, f.stream.OwnerID, &receipt)
	if err != nil {
		t.Fatalf("committed cancellation lost its physical signal: %v", err)
	}
	if !errors.Is(context.Cause(childCtx), ErrGenerationStopped) {
		t.Fatalf("exact committed signal=%v", context.Cause(childCtx))
	}
	if siblingCtx.Err() != nil {
		t.Fatal("disconnected Stop cancelled sibling")
	}
	if _, err := f.repo.ValidateLiveRunnerClaim(t.Context(), siblingClaim); err != nil {
		t.Fatalf("sibling claim changed: %v", err)
	}
	finishChild()
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("owned registration completion was not observed")
	}
	stored, found, err := f.store.ReadDelegatedStop(t.Context(), f.stream.OwnerID, f.stream.FrameID, request.RequestID)
	if err != nil || !found || stored.State != "settled" || stored.Targets[0].ExecutionObservation != "stopped" {
		t.Fatalf("committed physical receipt=%#v found=%t err=%v", stored, found, err)
	}
}

func TestDelegatedStopDoesNotTransferSignalToReplacementSupervisor(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "replacement-supervisor",
		Requests: []workspace.KernelDelegateRequest{{Task: "Exact original task"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var originalStopped, replacementStopped atomic.Bool
	original := &kernelChildRun{done: make(chan struct{}), stop: func() { originalStopped.Store(true) }}
	_, _, finish := delegatedActiveRun(t, f, children[0], "original-run", original)
	request, _ := delegatedAPIRequest(t, f, children[0].FrameID)
	receipt, err := f.store.AdmitDelegatedStop(t.Context(), f.stream.OwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	key := kernelChildRunKey{server: f.server, frameID: children[0].FrameID}
	originalTarget, replacementTarget := *request.Target, *request.Target
	replacementTarget.RunnerAttempt++
	original.target = &originalTarget
	replacement := &kernelChildRun{target: &replacementTarget, done: make(chan struct{}), stop: func() { replacementStopped.Store(true) }}
	kernelChildRuns.Store(key, original)
	defer kernelChildRuns.Delete(key)
	defer close(original.done)
	defer close(replacement.done)
	f.server.sessionRunsMu.Lock()
	active := f.server.sessionRuns[children[0].FrameID]
	previousCancel := active.cancel
	// Deterministic registration turnover at the actual signal seam, without
	// a sleep or a production hook. The later registration is not authorized
	// by the earlier runner's exact canonical cancellation proof.
	active.cancel = func(cause error) {
		previousCancel(cause)
		kernelChildRuns.Store(key, replacement)
	}
	f.server.sessionRunsMu.Unlock()
	_, err = f.server.signalDelegatedStop(t.Context(), f.stream.OwnerID, &receipt)
	if err != nil {
		t.Fatal(err)
	}
	finish()
	if replacementStopped.Load() {
		t.Fatal("old exact-run proof was transferred to a replacement supervisor")
	}
	if !originalStopped.Load() {
		t.Fatal("original owned supervisor did not receive its exact stop")
	}
}
