package server

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestDelegatedTerminalPreservesParentSiblingAndIndependentManualCell(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true, kernelHostTestRuntimeOptions{CanonicalTranscript: true})
	defer closeKernelHostTestRuntime(t, app, manager, store)
	// Establish the real parent authority before delegation creates rich
	// legacy-compatible parent observations; CreateStream cannot import those.
	root, err := app.transcriptStore.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "frame:" + identity.access.Frame.ID, OwnerID: identity.access.UserID,
		ExternalID: identity.access.Frame.ID, SessionID: identity.access.Frame.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: identity.access.Frame.ProjectID,
		RootFrameID: identity.access.Frame.RootFrameID, FrameID: identity.access.Frame.ID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	children, err := store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: identity.access.Frame.ID, OwnerUserID: identity.access.UserID, ToolUseID: "independent-manual",
		Requests: []workspace.KernelDelegateRequest{{Task: "Finished child"}, {Task: "Unrelated sibling"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	specs := make([]kernelruntime.SessionSpec, 0, 3)
	kernels := make([]kernelruntime.LocalExecutionTarget, 0, 3)
	for _, id := range []string{identity.access.Frame.ID, children[0].FrameID, children[1].FrameID} {
		access, found, err := store.GetKernelFrameAccess(id)
		if err != nil || !found {
			t.Fatalf("frame access found=%t err=%v", found, err)
		}
		spec := kernelruntime.SessionSpec{OwnerID: access.UserID, ProjectID: access.Frame.ProjectID,
			FrameID: id, FrameIncarnationID: access.Frame.IncarnationID, RootFrameID: access.Frame.RootFrameID,
			RootFrameIncarnationID: access.RootFrameIncarnationID, KernelKind: "analysis", Language: "python", Environment: "python", WorkspaceDir: t.TempDir()}
		ensured, err := manager.EnsureSession(spec)
		if err != nil {
			t.Fatal(err)
		}
		target, found := manager.LocalExecutionTarget(ensured.ID)
		if !found {
			t.Fatal("real worker has no exact target")
		}
		specs, kernels = append(specs, spec), append(kernels, target)
	}
	target := kernels[1]
	handle, err := manager.Submit(kernelruntime.SubmitRequest{KernelID: target.KernelID, ExpectedGeneration: target.Generation,
		OwnerID: target.OwnerID, ProjectID: target.ProjectID, FrameID: target.FrameID, FrameIncarnationID: target.FrameIncarnationID,
		RootFrameIncarnationID: target.RootFrameIncarnationID, KernelKind: target.KernelKind, Language: target.Language,
		Environment: target.Environment, ExecID: "independent-manual-cell", ToolUseID: "manual-survival-tool", Origin: "user", Timeout: 15 * time.Second,
		Code: fmt.Sprintf("import os, time\nwhile not os.path.exists(%q):\n    time.sleep(0.01)\n", filepath.Join(specs[1].WorkspaceDir, "release"))})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.InterruptExecution(handle.Identity())
	select {
	case <-handle.Started():
	case <-time.After(5 * time.Second):
		t.Fatal("manual cell did not start")
	}
	stream, found, err := app.transcriptStore.GetFrameStreamBySession(t.Context(), identity.access.UserID, children[0].FrameID)
	if err != nil || !found {
		t.Fatalf("child stream found=%t err=%v", found, err)
	}
	if err := app.releaseSessionRunnerKernels(t.Context(), children[0].FrameID, &transcriptRunnerAuthority{Stream: stream}); err != nil {
		t.Fatal(err)
	}
	for _, original := range kernels {
		current, found := manager.LocalExecutionTarget(original.KernelID)
		if !found || current != original {
			t.Fatalf("child terminal changed unrelated/shared worker: original=%#v current=%#v", original, current)
		}
	}
	if observed, found := manager.ObserveExecution(handle.Identity()); !found || observed.Status != "running" {
		t.Fatalf("child terminal stopped independent manual cell: %#v found=%t", observed, found)
	}
	// Root task cleanup still owns its whole tree, including these workers.
	if err := app.releaseSessionRunnerKernels(context.Background(), root.FrameID, &transcriptRunnerAuthority{Stream: root}); err != nil {
		t.Fatal(err)
	}
	if remaining := manager.ListSessionKernels(root.FrameID); len(remaining) != 0 {
		t.Fatalf("root cleanup left workers: %#v", remaining)
	}
}
