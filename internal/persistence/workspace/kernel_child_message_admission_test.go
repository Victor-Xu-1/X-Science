package workspace

import (
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestDelegatedChildMessageCanonicalAdmissionIsIdempotentAndScoped(t *testing.T) {
	store, repo, parent, _ := newTranscriptCancellationFixture(t)
	children, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{
		ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "canonical-followup",
		Requests: []KernelDelegateRequest{{Task: "Original task"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	child := children[0]
	oldStop := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "child", child.FrameID)
	queued, err := store.QueueKernelSupervisionMessageWithID(t.Context(), parent.FrameID, child.FrameID, parent.OwnerID, "frozen-followup", "Exact queued follow-up", "question")
	if err != nil || queued.Queued == nil {
		t.Fatalf("queue=%#v err=%v", queued, err)
	}
	target, _, err := store.KernelChildDispatchTarget(t.Context(), parent.OwnerID, parent.FrameID, child.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	changed := *queued.Queued
	changed.Message = "Forged replacement"
	if err := store.AdmitKernelChildMessageInputs(t.Context(), child, target, []KernelChildQueuedMessage{changed}); !errors.Is(err, transcriptstore.ErrEventConflict) {
		t.Fatalf("forged queue accepted: %v", err)
	}
	stale := target
	stale.FrameIncarnationID = "old-incarnation"
	if err := store.AdmitKernelChildMessageInputs(t.Context(), child, stale, []KernelChildQueuedMessage{*queued.Queued}); !errors.Is(err, ErrDelegatedStopScopeStale) {
		t.Fatalf("stale scope accepted: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := store.AdmitKernelChildMessageInputs(t.Context(), child, target, []KernelChildQueuedMessage{*queued.Queued}); err != nil {
			t.Fatal(err)
		}
	}
	stream, err := repo.GetStream(t.Context(), target.StreamUID, parent.OwnerID)
	if err != nil || stream.InputRevision != 2 {
		t.Fatalf("queue replay duplicated input: %#v %v", stream, err)
	}
	if _, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, oldStop); !errors.Is(err, ErrDelegatedStopOriginUnavailable) && !errors.Is(err, ErrDelegatedStopTargetStale) {
		t.Fatalf("old Stop touched queued/new input: %v", err)
	}
	claim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: parent.OwnerID,
		RunnerID: "followup-run", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
		ExpectedInputMessageID: "kernel-child-message:" + queued.Queued.ID, ExpectedInputRevision: 2,
		ExpectedFrameIncarnationID: target.FrameIncarnationID, ExpectedStreamEpoch: target.StreamEpoch})
	if err != nil || !claim.Claimed {
		t.Fatalf("canonical follow-up not claimable: %#v %v", claim, err)
	}
	if pending, err := store.KernelChildHasPendingMessages(t.Context(), child.FrameID, parent.OwnerID); err != nil || !pending {
		t.Fatalf("materialization consumed queue prematurely: %t %v", pending, err)
	}
	if err := store.MarkKernelChildMessageConsumed(t.Context(), child.FrameID, parent.OwnerID, queued.Queued.ID, queued.Queued.Generation); err != nil {
		t.Fatal(err)
	}
}
