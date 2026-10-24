package server

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestKernelCollectCancellationDoesNotStopAcceptedChild(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	child, initial := createKernelCancellationBoundaryChild(t, f, "collect-only-cancel")
	beforeFrame, found, err := f.store.GetFrame(child.FrameID)
	if err != nil || !found {
		t.Fatalf("SETUP_FAILURE: child frame found=%t err=%v", found, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := f.server.collectKernelChildren(ctx, f.identity.access, []string{child.FrameID}, []workspace.KernelSupervisedChild{child}, []bool{true}, time.Hour)
	if len(results) != 1 {
		t.Fatalf("collect cancellation returned %d results", len(results))
	}
	result, ok := results[0].(map[string]any)
	if !ok || result["status"] != "cancelled" || result["error"] != "collect cancelled" || result["stopped_by_parent"] == true {
		t.Errorf("collect cancellation claimed a child stop: %#v", results[0])
	}
	after, found, err := f.store.GetKernelSupervisedChild(context.Background(), child.ParentFrameID, child.FrameID, child.OwnerUserID)
	if err != nil || !found || !reflect.DeepEqual(after, child) {
		t.Errorf("collect cancellation changed the child: before=%#v after=%#v found=%t err=%v", child, after, found, err)
	}
	afterFrame, found, err := f.store.GetFrame(child.FrameID)
	if err != nil || !found || !reflect.DeepEqual(beforeFrame, afterFrame) {
		t.Errorf("collect cancellation changed the child frame: before=%#v after=%#v found=%t err=%v", beforeFrame, afterFrame, found, err)
	}
	var receipts int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=?`, initial.StreamUID).Scan(&receipts); err != nil || receipts != 0 {
		t.Errorf("collect cancellation created %d child terminal receipts: %v", receipts, err)
	}
}

func TestKernelBlockingCancellationPreservesNewerChildInput(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	child, initial := createKernelCancellationBoundaryChild(t, f, "newer-input-cancel")
	ctx := context.Background()
	if _, _, created, err := f.repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: initial.StreamUID, OwnerID: child.OwnerUserID, ClientMessageID: "new-child-request",
		FrameEventID: "new-child-request-event", MessageUUID: "new-child-request-message", Text: child.Task,
	}); err != nil || !created {
		t.Fatalf("SETUP_FAILURE: new child input created=%t err=%v", created, err)
	}
	claimed, err := f.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: initial.StreamUID, OwnerID: child.OwnerUserID, RunnerID: "new-child-owner", TTL: time.Minute,
		ResumeSource:           transcriptstore.ResumeSourceFresh,
		ExpectedInputMessageID: "new-child-request", ExpectedInputRevision: initial.InputRevision + 1,
		ExpectedFrameIncarnationID: initial.FrameIncarnationID, ExpectedStreamEpoch: initial.StreamEpoch,
	})
	if err != nil || !claimed.Claimed || claimed.Claim.ClaimedInputRevision != initial.InputRevision+1 {
		t.Fatalf("SETUP_FAILURE: new child claim=%#v err=%v", claimed, err)
	}
	beforeFrame, found, err := f.store.GetFrame(child.FrameID)
	if err != nil || !found {
		t.Fatalf("SETUP_FAILURE: current child frame found=%t err=%v", found, err)
	}
	parentCtx, cancel := context.WithCancel(ctx)
	cancel()
	results := f.server.waitKernelChildren(parentCtx, f.identity.access, []workspace.KernelSupervisedChild{child}, 0, true)
	if len(results) != 1 {
		t.Fatalf("old parent cancellation returned %d results", len(results))
	}
	result, ok := results[0].(map[string]any)
	if !ok || result["frame_id"] != child.FrameID || result["status"] != "failed" || stringValue(result["error"]) == "" || result["stopped_by_parent"] == true {
		t.Errorf("old parent cancellation did not report the rejected original target: %#v", results[0])
	}
	after, found, err := f.store.GetKernelSupervisedChild(ctx, child.ParentFrameID, child.FrameID, child.OwnerUserID)
	if err != nil || !found || !reflect.DeepEqual(after, child) {
		t.Errorf("old parent cancellation changed the newer child's summary: before=%#v after=%#v found=%t err=%v", child, after, found, err)
	}
	afterFrame, found, err := f.store.GetFrame(child.FrameID)
	if err != nil || !found || !reflect.DeepEqual(beforeFrame, afterFrame) {
		t.Errorf("old parent cancellation changed the newer child's frame: before=%#v after=%#v found=%t err=%v", beforeFrame, afterFrame, found, err)
	}
	if _, err := f.repo.ValidateLiveRunnerClaim(ctx, claimed.Claim); err != nil {
		t.Errorf("old parent cancellation invalidated the newer claim: %v", err)
	}
	var receipts int
	if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=?`, initial.StreamUID).Scan(&receipts); err != nil || receipts != 0 {
		t.Errorf("old parent cancellation created %d newer-child terminal receipts: %v", receipts, err)
	}
}

func createKernelCancellationBoundaryChild(t *testing.T, f *agentSaveArtifactsFixture, callID string) (workspace.KernelSupervisedChild, *workspace.DelegatedStopTarget) {
	t.Helper()
	children, err := f.store.CreateKernelSupervisedChildren(context.Background(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: callID,
		Requests: []workspace.KernelDelegateRequest{{Task: "Original delegated input"}},
	})
	if err != nil || len(children) != 1 {
		t.Fatalf("SETUP_FAILURE: actual child creation=%#v err=%v", children, err)
	}
	initial, err := f.store.KernelChildInitialTarget(context.Background(), children[0])
	if err != nil || initial == nil || initial.RunnerAttempt != 0 || initial.InputRevision != 1 {
		t.Fatalf("SETUP_FAILURE: committed child origin=%#v err=%v", initial, err)
	}
	return children[0], initial
}

func TestKernelBlockingCancellationConfirmsOriginalOwnedRun(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	child, _ := createKernelCancellationBoundaryChild(t, f, "owned-run-cancel")
	childCtx, claim, finish := delegatedActiveRun(t, f, child, "owned-blocking-child")
	f.server.sessionRunsMu.Lock()
	active := f.server.sessionRuns[child.FrameID]
	f.server.sessionRunsMu.Unlock()
	if active == nil || active.done == nil {
		t.Fatal("SETUP_FAILURE: real child execution owner was not registered")
	}
	ownerExited := make(chan struct{})
	go func() {
		<-childCtx.Done()
		finish()
		close(ownerExited)
	}()
	t.Cleanup(func() {
		// Release the real owner on a failed assertion too. All success
		// observations below happen before this cleanup can run.
		finish()
		select {
		case <-ownerExited:
		case <-time.After(3 * time.Second):
			t.Error("owned child goroutine was not released during cleanup")
		}
	})
	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()
	results := f.server.waitKernelChildren(parentCtx, f.identity.access, []workspace.KernelSupervisedChild{child}, 0, true)
	if len(results) != 1 {
		t.Fatalf("owned cancellation returned %d results", len(results))
	}
	result, ok := results[0].(map[string]any)
	if !ok || result["frame_id"] != child.FrameID || result["status"] != "cancelled" || result["execution_observation"] != "stopped" || result["stopped_by_parent"] != true {
		t.Fatalf("actual owned stop was not confirmed by the blocking consumer: %#v", results[0])
	}
	if !errors.Is(context.Cause(childCtx), ErrGenerationStopped) {
		t.Errorf("original child owner received cancellation cause %v", context.Cause(childCtx))
	}
	select {
	case <-active.done:
	default:
		t.Error("confirmed cancellation returned before the original registration's done closed")
	}
	select {
	case <-ownerExited:
	case <-time.After(3 * time.Second):
		t.Error("confirmed cancellation did not release the actual owned goroutine")
	}
	receipt, found, err := f.store.ReadDelegatedStop(context.Background(), child.OwnerUserID, child.ParentFrameID, stringValue(result["request_id"]))
	if err != nil || !found || receipt.State != "settled" || len(receipt.Targets) != 1 {
		t.Fatalf("durable original Stop receipt=%#v found=%t err=%v", receipt, found, err)
	}
	outcome := receipt.Targets[0]
	if outcome.Outcome != "cancelled" || outcome.ExecutionObservation != "stopped" || outcome.Target.RunnerAttempt != claim.Attempt || outcome.Target.InputRevision != claim.ClaimedInputRevision {
		t.Errorf("durable Stop did not confirm the original claimed owner: %#v", outcome)
	}

	repeated := f.server.waitKernelChildren(parentCtx, f.identity.access, []workspace.KernelSupervisedChild{child}, 0, true)
	if len(repeated) != 1 {
		t.Fatalf("same cancellation retry returned %d results", len(repeated))
	}
	replayed, ok := repeated[0].(map[string]any)
	if !ok || replayed["request_id"] != result["request_id"] || replayed["status"] != "cancelled" || replayed["execution_observation"] != "stopped" {
		t.Errorf("same original cancellation was not replayed: first=%#v repeated=%#v", result, repeated[0])
	}
	var terminalEvents, terminalReceipts int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='runner_finished'`, outcome.Target.StreamUID).Scan(&terminalEvents); err != nil || terminalEvents != 1 {
		t.Errorf("same cancellation retry left %d canonical terminal events: %v", terminalEvents, err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=? AND status='cancelled'`, outcome.Target.StreamUID).Scan(&terminalReceipts); err != nil || terminalReceipts != 1 {
		t.Errorf("same cancellation retry left %d canonical receipts: %v", terminalReceipts, err)
	}
	callID := "blocking-delegate-cancel:" + child.ToolUseID + ":" + child.FrameID
	for _, variant := range []string{"another-input", "another-positive-attempt"} {
		invalid := outcome.Target
		if variant == "another-input" {
			invalid.InputRevision++
		} else {
			invalid.RunnerAttempt = claim.Attempt + 1
		}
		if _, err := f.store.AdmitKernelChildStop(context.Background(), child.OwnerUserID, receipt.Request.Scope, child.FrameID, "blocking delegation was cancelled", callID, &invalid); !errors.Is(err, workspace.ErrDelegatedStopTargetStale) {
			t.Errorf("same receipt accepted %s: %v", variant, err)
		}
	}
}
