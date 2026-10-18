package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

func delegatedStopRequestForTest(t *testing.T, store *Store, parent, owner, mode, child string) DelegatedStopRequest {
	t.Helper()
	snapshot, err := store.DelegatedStopSnapshot(t.Context(), owner, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	request := DelegatedStopRequest{Schema: "x-science.delegated-stop.v1", RequestID: uuid.NewString(), Scope: snapshot.Scope, Branch: snapshot.Branch,
		ParentInputRevision: snapshot.ParentInputRevision, ExpectedFenceGeneration: snapshot.FenceGeneration, Mode: mode}
	if mode == "child" {
		for _, item := range snapshot.Targets {
			if item.FrameID == child {
				request.Target = item.Target
			}
		}
		if request.Target == nil {
			t.Fatal("missing exact child target")
		}
	}
	return request
}

func TestDelegatedStopPreclaimReplayNeverTargetsNewInput(t *testing.T) {
	store, repo, parent, _ := newTranscriptCancellationFixture(t)
	children, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "preclaim",
		Requests: []KernelDelegateRequest{{Task: "Original input"}}})
	if err != nil {
		t.Fatal(err)
	}
	request := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "child", children[0].FrameID)
	if request.Target.RunnerAttempt != 0 {
		t.Fatal("fixture already claimed")
	}
	first, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, request)
	if err != nil || len(first.Targets) != 1 || first.Targets[0].Outcome != "cancel_requested" {
		t.Fatalf("first=%#v %v", first, err)
	}
	late, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: request.Target.StreamUID, OwnerID: parent.OwnerID, RunnerID: "late-dispatch", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh, ExpectedInputMessageID: request.Target.InputMessageID, ExpectedInputRevision: request.Target.InputRevision,
		ExpectedFrameIncarnationID: request.Target.FrameIncarnationID, ExpectedStreamEpoch: request.Target.StreamEpoch})
	if err == nil && late.Claimed {
		t.Fatal("late dispatch claimed cancelled initial input")
	}
	_, _, _, err = repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: request.Target.StreamUID, OwnerID: parent.OwnerID,
		ClientMessageID: "new-input", FrameEventID: "new-input-event", MessageUUID: "new-input-message", Text: "Explicit new task", Destinations: []string{"ws"}})
	if err != nil {
		t.Fatal(err)
	}
	newClaim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: request.Target.StreamUID, OwnerID: parent.OwnerID, RunnerID: "new-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !newClaim.Claimed {
		t.Fatalf("new claim=%#v %v", newClaim, err)
	}
	replay, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, request)
	if err != nil || !replay.Replayed || replay.Targets[0].Target != *request.Target {
		t.Fatalf("replay=%#v %v", replay, err)
	}
	if allowed, err := store.DelegatedStopSignalAllowed(t.Context(), parent.OwnerID, replay, *request.Target, &newClaim.Claim); err != nil || allowed {
		t.Fatalf("old receipt signals new input=%t %v", allowed, err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(t.Context(), newClaim.Claim); err != nil {
		t.Fatalf("replay touched new claim: %v", err)
	}
	changed := request
	changed.Mode, changed.Target = "branch", nil
	if _, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, changed); !errors.Is(err, ErrDelegatedStopRequestConflict) {
		t.Fatalf("changed request=%v", err)
	}
}

func TestDelegatedStopUsesActiveStreamNotHigherEpoch(t *testing.T) {
	store, repo, parent, _ := newTranscriptCancellationFixture(t)
	children, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "stream-fence",
		Requests: []KernelDelegateRequest{{Task: "Initial"}}})
	if err != nil {
		t.Fatal(err)
	}
	request := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "child", children[0].FrameID)
	other, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: "inactive:" + children[0].FrameID, OwnerID: parent.OwnerID, ExternalID: "inactive:" + children[0].FrameID,
		SessionID: children[0].FrameID, Kind: transcriptstore.StreamKindFrameRef, ProjectID: parent.ProjectID, RootFrameID: parent.RootFrameID, FrameID: children[0].FrameID, Epoch: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, request); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_attempts WHERE stream_uid=?`, other.UID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("inactive stream mutated count=%d err=%v", count, err)
	}
	var status string
	if err := store.db.QueryRow(`SELECT status FROM transcript_runner_attempts WHERE stream_uid=?`, request.Target.StreamUID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("active stream=%s %v", status, err)
	}
}

func TestDelegationInitialInputAtomicAndEquivalent(t *testing.T) {
	store, repo, parent, _ := newTranscriptCancellationFixture(t)
	input := CreateKernelDelegatesInput{ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "initial-input",
		Requests: []KernelDelegateRequest{{Task: "Answer", ContextSummary: "Evidence", Model: "model", OutputSchema: map[string]any{"type": "object"}}}}
	children, err := store.CreateKernelSupervisedChildren(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if has, err := store.KernelChildHasCanonicalInitialInput(t.Context(), children[0]); err != nil || !has {
		t.Fatalf("canonical=%t %v", has, err)
	}
	stream, found, err := repo.GetFrameStreamBySession(t.Context(), parent.OwnerID, children[0].FrameID)
	if err != nil || !found || stream.InputRevision != 1 {
		t.Fatalf("stream=%#v %v", stream, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='user_message'`, stream.UID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("initial inputs=%d %v", count, err)
	}
	input.ToolUseID = "rollback-input"
	input.Requests = []KernelDelegateRequest{{Task: "First"}, {Task: " "}}
	if _, err := store.CreateKernelSupervisedChildren(context.Background(), input); err == nil {
		t.Fatal("invalid second request accepted")
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM kernel_child_supervision WHERE tool_use_id=?`, input.ToolUseID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial children=%d %v", count, err)
	}
}

func TestDelegatedStopUnclaimedFollowupHasOwnCancellationAuthority(t *testing.T) {
	store, repo, parent, _ := newTranscriptCancellationFixture(t)
	children, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "followup-stop", Requests: []KernelDelegateRequest{{Task: "Initial"}}})
	if err != nil {
		t.Fatal(err)
	}
	first := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "child", children[0].FrameID)
	if _, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, first); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: first.Target.StreamUID, OwnerID: parent.OwnerID, ClientMessageID: "followup", FrameEventID: "followup-event", MessageUUID: "followup-uuid", Text: "A new explicit task", Destinations: []string{"ws"}}); err != nil {
		t.Fatal(err)
	}
	next := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "child", children[0].FrameID)
	if next.Target.RunnerAttempt != 0 || next.Target.InputRevision <= first.Target.InputRevision {
		t.Fatalf("new unclaimed target=%#v", next.Target)
	}
	if _, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, next); err != nil {
		t.Fatal(err)
	}
	stream, err := repo.GetStream(t.Context(), first.Target.StreamUID, parent.OwnerID)
	if err != nil || stream.ConsumedInputRevision != next.Target.InputRevision {
		t.Fatalf("new input not fenced=%#v %v", stream, err)
	}
	var receipts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=? AND status='cancelled'`, stream.UID).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("distinct input receipts=%d %v", receipts, err)
	}
	late, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: parent.OwnerID, RunnerID: "late-followup", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err == nil && late.Claimed {
		t.Fatal("late worker started cancelled followup")
	}
}
