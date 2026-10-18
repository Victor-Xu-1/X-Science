package workspace

import (
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestKernelChildCompletionFenceRejectsStaleClaimAndNewInput(t *testing.T) {
	store, repo, parent, _ := newTranscriptCancellationFixture(t)
	children, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{
		ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "completion-fence",
		Requests: []KernelDelegateRequest{{Task: "Original"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	child := children[0]
	stream, found, err := repo.GetFrameStreamBySession(t.Context(), parent.OwnerID, child.FrameID)
	if err != nil || !found {
		t.Fatalf("stream found=%t err=%v", found, err)
	}
	claimed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: parent.OwnerID,
		RunnerID: "exact-child-owner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	request := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "child", child.FrameID)
	if _, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: claimed.Claim,
		ClientMessageID: "exact-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`)}); err != nil {
		t.Fatal(err)
	}
	fence := KernelChildCompletionFence{Target: *request.Target, Claim: claimed.Claim}
	wrong := fence
	wrong.Claim.ClaimToken = "not-the-owned-claim"
	if _, err := store.CompleteKernelSupervisedChild(t.Context(), child.FrameID, parent.OwnerID, "completed", map[string]any{"response": "forged"}, "", wrong); !errors.Is(err, transcriptstore.ErrClaimStale) {
		t.Fatalf("wrong token projected completion: %v", err)
	}
	if _, err := store.CompleteKernelSupervisedChild(t.Context(), child.FrameID, parent.OwnerID, "completed", map[string]any{"response": "original result"}, "", fence); err != nil {
		t.Fatal(err)
	}
	queued, err := store.QueueKernelSupervisionMessageWithID(t.Context(), parent.FrameID, child.FrameID, parent.OwnerID, "new-after-finish", "Next input", "info")
	if err != nil {
		t.Fatal(err)
	}
	newTarget, _, err := store.KernelChildDispatchTarget(t.Context(), parent.OwnerID, parent.FrameID, child.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitKernelChildMessageInputs(t.Context(), child, newTarget, []KernelChildQueuedMessage{*queued.Queued}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteKernelSupervisedChild(t.Context(), child.FrameID, parent.OwnerID, "completed", nil, "", fence); !errors.Is(err, transcriptstore.ErrClaimStale) {
		t.Fatalf("old completion overwrote new input: %v", err)
	}
	current, found, err := store.GetFrame(child.FrameID)
	if err != nil || !found || current.Status != "processing" {
		t.Fatalf("new input state lost: %#v found=%t err=%v", current, found, err)
	}
}
