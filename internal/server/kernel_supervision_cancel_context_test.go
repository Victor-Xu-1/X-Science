package server

import (
	"context"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestKernelBlockingDelegateCancelledBeforeWaitPersistsOriginalChild(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	children, err := f.store.CreateKernelSupervisedChildren(ctx, workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "cancel-before-wait",
		Requests: []workspace.KernelDelegateRequest{{Task: "Original queued delegation"}},
	})
	if err != nil || len(children) != 1 {
		t.Fatalf("SETUP_FAILURE: actual child creation=%#v err=%v", children, err)
	}
	child := children[0]
	initial, err := f.store.KernelChildInitialTarget(ctx, child)
	if err != nil || initial == nil || initial.RunnerAttempt != 0 || initial.InputRevision != 1 {
		t.Fatalf("SETUP_FAILURE: committed canonical initial target=%#v err=%v", initial, err)
	}

	// This is the admitted interleaving in which child creation is already
	// visible, but the parent cancels before the blocking wait captures origin.
	// The fixture has no KernelManager, provider or child execution goroutine.
	parentCtx, cancel := context.WithCancel(ctx)
	cancel()
	results := f.server.waitKernelChildren(parentCtx, f.identity.access, children, 0, true)
	if len(results) != 1 {
		t.Errorf("cancelled delegation returned %d results; want one", len(results))
	} else if result, ok := results[0].(map[string]any); !ok || result["frame_id"] != child.FrameID || result["status"] != "stopping" || result["execution_observation"] != "unavailable" {
		t.Errorf("queued cancellation did not retain its unconfirmed physical stop: %#v", results[0])
	}

	settled, found, err := f.store.GetKernelSupervisedChild(ctx, child.ParentFrameID, child.FrameID, child.OwnerUserID)
	if err != nil || !found || settled.Status != "completed" || settled.CompletedAt == nil || settled.Output["stopped_by_parent"] != true || kernelChildPublicStatus(settled) != "cancelled" {
		t.Errorf("parent cancellation lost the accepted child: summary=%#v found=%t err=%v", settled, found, err)
	}
	frame, found, err := f.store.GetFrame(child.FrameID)
	if err != nil || !found || frame.Status != "cancelled" {
		t.Errorf("parent cancellation left the child frame active: frame=%#v found=%t err=%v", frame, found, err)
	}
	state, found, err := f.repo.GetLatestRunnerRuntimeState(ctx, initial.StreamUID, child.OwnerUserID)
	if err != nil || !found || state.Status != "cancelled" {
		t.Errorf("parent cancellation did not persist the canonical terminal: state=%#v found=%t err=%v", state, found, err)
	}
	var cancelledReceipts int
	if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=? AND status='cancelled'`, initial.StreamUID).Scan(&cancelledReceipts); err != nil || cancelledReceipts != 1 {
		t.Errorf("canonical cancellation receipts=%d err=%v; want one", cancelledReceipts, err)
	}
	if _, err := f.repo.ValidateLiveRunnerClaim(ctx, f.claim); err != nil {
		t.Errorf("child cancellation changed its parent's canonical claim: %v", err)
	}
}
