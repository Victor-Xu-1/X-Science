package workspace

import (
	"context"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestDelegatedStopCanonicalTerminalPreservesParentAndSibling(t *testing.T) {
	store, repo, parent, parentClaim := newTranscriptCancellationFixture(t)
	ctx := context.Background()
	children, err := store.CreateKernelSupervisedChildren(ctx, CreateKernelDelegatesInput{
		ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "stop-contract-delegate",
		Requests: []KernelDelegateRequest{{Task: "Target child"}, {Task: "Unaffected sibling"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	childStream, childClaim := claimDelegatedStopTestChild(t, repo, children[0])
	siblingStream, siblingClaim := claimDelegatedStopTestChild(t, repo, children[1])
	result, err := store.StopKernelSupervisedChildTree(ctx, parent.FrameID, children[0].FrameID, parent.OwnerID, "Explicit child stop")
	if err != nil || !result.Applied {
		t.Fatalf("actual child stop=%#v err=%v", result, err)
	}
	for _, target := range []struct {
		stream transcriptstore.Stream
		claim  transcriptstore.RunnerClaim
		status string
	}{{parent, parentClaim, "running"}, {siblingStream, siblingClaim, "running"}, {childStream, childClaim, "cancelled"}} {
		state, err := repo.GetRunnerRuntimeState(ctx, target.stream.UID, target.stream.OwnerID, target.claim.Attempt)
		if err != nil || state.Status != target.status {
			t.Errorf("canonical runner %s=%#v err=%v; want %s", target.stream.FrameID, state, err, target.status)
		}
	}
	if _, err := repo.ValidateLiveRunnerClaim(ctx, childClaim); err == nil {
		t.Error("stopped child retained a writable live canonical claim")
	}
	var terminalReceipts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=? AND status='cancelled'`, childStream.UID).Scan(&terminalReceipts); err != nil || terminalReceipts != 1 {
		t.Errorf("child stop has %d canonical cancellation receipts, err=%v", terminalReceipts, err)
	}
}

func claimDelegatedStopTestChild(t *testing.T, repo *transcriptstore.Repository, child KernelSupervisedChild) (transcriptstore.Stream, transcriptstore.RunnerClaim) {
	t.Helper()
	ctx := context.Background()
	stream, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "frame:" + child.FrameID, OwnerID: child.OwnerUserID, ExternalID: child.FrameID, SessionID: child.FrameID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: child.ProjectID, RootFrameID: child.RootFrameID, FrameID: child.FrameID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "input-" + child.FrameID,
		FrameEventID: "input-event-" + child.FrameID, MessageUUID: "input-message-" + child.FrameID,
		Text: child.Task, Destinations: []string{"ws"},
	}); err != nil || !created {
		t.Fatalf("append actual child input=%t %v", created, err)
	}
	claim, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "runner-" + child.FrameID,
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim child=%#v %v", claim, err)
	}
	return stream, claim.Claim
}
