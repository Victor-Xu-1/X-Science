package server

import (
	"context"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualContextCompactionDrainWaitsForExactOwnerWithoutStoppingChild(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	child, err := f.store.CreateFrame(workspace.CreateFrameInput{ID: "independent-child", ProjectID: f.stream.ProjectID,
		ParentFrameID: f.stream.FrameID, AgentName: "OPERON", Status: "processing", ConversationType: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	childStream, err := f.repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "frame:" + child.ID, OwnerID: f.stream.OwnerID, ExternalID: child.ID, SessionID: child.ID, Kind: transcriptstore.StreamKindFrameRef,
		ProjectID: child.ProjectID, RootFrameID: child.RootFrameID, FrameID: child.ID, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: childStream.UID, OwnerID: childStream.OwnerID, ClientMessageID: "child-work", FrameEventID: "child-event", MessageUUID: "child-message", Text: "Independent child work"})
	if err != nil {
		t.Fatal(err)
	}
	childClaim, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{StreamUID: childStream.UID, OwnerID: childStream.OwnerID,
		RunnerID: "child-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !childClaim.Claimed {
		t.Fatalf("child claim=%#v err=%v", childClaim, err)
	}
	childCtx, childRun, finishChild, err := f.server.registerActiveSessionRun(context.Background(), child.ID, "child-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishChild()
	childRun.transcript = &transcriptRunnerAuthority{Stream: childStream, Claim: childClaim.Claim}
	// The independently running child is not a root-wide maintenance blocker.
	request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
	if _, err = f.store.AdmitContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request); err != nil {
		t.Fatal(err)
	}
	run := claimSkillControlInput(t, f)
	runCtx, active, finish, err := f.server.registerActiveSessionRun(context.Background(), f.stream.FrameID, f.claim.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	active.transcript = run.Transcript
	type result struct {
		receipt transcriptstore.ContextCompactionReceipt
		err     error
	}
	completed := make(chan result, 1)
	go func() {
		receipt, err := f.server.cancelContextCompactionAndDrain(context.Background(), f.stream.OwnerID, f.stream.FrameID, request)
		completed <- result{receipt, err}
	}()
	select {
	case <-runCtx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("owned cancellation not signalled")
	}
	receipt, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request)
	if err != nil {
		t.Fatal(err)
	}
	f.server.observeContextCompaction(&receipt)
	if receipt.ExecutionObservation != "draining" || receipt.CanSubmitNextInput {
		t.Fatalf("live owner not drained: %#v", receipt)
	}
	select {
	case <-active.done:
		t.Fatal("request handler closed another owner's done")
	default:
	}
	select {
	case <-childCtx.Done():
		t.Fatal("maintenance cancelled an independent child")
	default:
	}
	before := map[string]bool{}
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		before[event.ID] = true
	}
	finish()
	foundNotice := false
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if !before[event.ID] && event.Type == "compaction_status" && event.Payload["request_id"] == request.RequestID {
			if event.FrameID != f.stream.FrameID || event.Payload["state"] != "cancelled" {
				t.Fatalf("cancelled owner cleanup published a different target: %#v", event)
			}
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Fatal("cancelled owner cleanup did not invalidate its draining receipt")
	}
	select {
	case settled := <-completed:
		if settled.err != nil || !settled.receipt.CanSubmitNextInput || settled.receipt.ExecutionObservation != "unavailable" {
			t.Fatalf("settled=%#v err=%v", settled.receipt, settled.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner cleanup did not release drain")
	}
	state, err := f.repo.GetRunnerRuntimeState(context.Background(), childStream.UID, childStream.OwnerID, childClaim.Claim.Attempt)
	if err != nil || state.Status != "running" {
		t.Fatalf("child state changed=%#v err=%v", state, err)
	}
}
