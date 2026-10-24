package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestKernelBlockingCancellationSignalsAllOwnersBeforePhysicalJoin(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(context.Background(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "parallel-owner-cancellation",
		Requests: []workspace.KernelDelegateRequest{{Task: "First delegated task"}, {Task: "Second delegated task"}},
	})
	if err != nil || len(children) != 2 {
		t.Fatalf("SETUP_FAILURE: actual parallel children=%#v err=%v", children, err)
	}
	ownerContexts := make([]context.Context, len(children))
	finishOwners := make([]func(), len(children))
	physicalGates := make([]chan struct{}, len(children))
	ownerExits := make([]chan struct{}, len(children))
	for index, child := range children {
		initial, err := f.store.KernelChildInitialTarget(context.Background(), child)
		if err != nil || initial == nil {
			t.Fatalf("SETUP_FAILURE: child %d canonical origin=%#v err=%v", index, initial, err)
		}
		ownerContexts[index], _, finishOwners[index] = delegatedActiveRun(t, f, child, "parallel-owner-"+child.FrameID)
		if ownerContexts[index].Err() != nil {
			t.Fatalf("SETUP_FAILURE: owner %d was already cancelled", index)
		}
		physicalGates[index], ownerExits[index] = make(chan struct{}), make(chan struct{})
	}
	var releaseOnce sync.Once
	releasePhysicalOwners := func() {
		releaseOnce.Do(func() {
			for _, gate := range physicalGates {
				close(gate)
			}
		})
	}
	waitDone := make(chan []any, 1)
	waitJoined := false
	t.Cleanup(func() {
		releasePhysicalOwners()
		for _, finish := range finishOwners {
			finish()
		}
		for index, exited := range ownerExits {
			select {
			case <-exited:
			case <-time.After(3 * time.Second):
				t.Errorf("owned child %d did not exit during cleanup", index)
			}
		}
		if !waitJoined {
			select {
			case <-waitDone:
				waitJoined = true
			case <-time.After(10 * time.Second):
				t.Error("blocking wait did not join after all physical owners were released")
			}
		}
	})
	signalled := make(chan int, len(children))
	for index := range children {
		go func(index int) {
			<-ownerContexts[index].Done()
			signalled <- index
			<-physicalGates[index]
			finishOwners[index]()
			close(ownerExits[index])
		}(index)
	}
	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()
	go func() {
		waitDone <- f.server.waitKernelChildren(parentCtx, f.identity.access, children, 0, true)
	}()
	// Every cancellation signal must arrive while both physical owners remain
	// blocked. A slow first owner's join cannot prevent a sibling's signal.
	seen := make(map[int]bool, len(children))
	guard := time.NewTimer(3 * time.Second)
	defer guard.Stop()
	for len(seen) < len(children) {
		select {
		case index := <-signalled:
			seen[index] = true
			if !errors.Is(context.Cause(ownerContexts[index]), ErrGenerationStopped) {
				t.Errorf("owner %d received cause %v", index, context.Cause(ownerContexts[index]))
			}
		case <-guard.C:
			t.Errorf("blocking cancellation signalled %d/%d owners before physical release; a sibling was delayed behind another owner's join", len(seen), len(children))
			return
		}
	}
	for index, exited := range ownerExits {
		select {
		case <-exited:
			t.Errorf("physical owner %d exited before its release gate opened", index)
		default:
		}
	}
	releasePhysicalOwners()
	select {
	case results := <-waitDone:
		waitJoined = true
		if len(results) != len(children) {
			t.Fatalf("parallel cancellation returned %d results", len(results))
		}
		for index, raw := range results {
			result, ok := raw.(map[string]any)
			if !ok || result["frame_id"] != children[index].FrameID {
				t.Errorf("parallel cancellation lost result position %d: %#v", index, raw)
			}
		}
	case <-time.After(10 * time.Second):
		t.Error("parallel cancellation did not return after both physical owners exited")
	}
}
