package workspace

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func conversationArchiveUpdate(frame Frame, archived bool, revision int64) ConversationArchiveUpdate {
	return ConversationArchiveUpdate{OwnerID: "archive-owner", FrameID: frame.ID, IncarnationID: frame.IncarnationID, ExpectedRevision: revision, Archived: archived}
}

func archiveFixtureClaim(t *testing.T, repo *transcriptstore.Repository, frame Frame) transcriptstore.RunnerClaim {
	t.Helper()
	if _, _, created, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: "frame:" + frame.ID, OwnerID: "archive-owner", ClientMessageID: "input-" + frame.ID,
		FrameEventID: "event-" + frame.ID, MessageUUID: "input-" + frame.ID, Text: "Continue the existing work",
	}); err != nil || !created {
		t.Fatalf("append created=%t err=%v", created, err)
	}
	claimed, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: "frame:" + frame.ID, OwnerID: "archive-owner", RunnerID: "runner-" + frame.ID,
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	return claimed.Claim
}

func TestConversationArchiveRejectsQueuedDeliveringWaitingAndUnclaimedDispatch(t *testing.T) {
	for _, state := range []string{"queued", "delivering", "awaiting_user_response", "awaiting_plan_approval", "dispatch"} {
		t.Run(state, func(t *testing.T) {
			store, _ := newConversationArchiveFixture(t)
			frame := conversationArchiveFixtureFrame(t, store, "archive-pending", 1, false, false)
			store.now = time.Now
			switch state {
			case "queued", "delivering":
				payload := map[string]any{"text": "Preserve queued user input"}
				if _, _, _, err := store.QueueCompatibilityMessage(frame.ID, "pending-queue-input", payload, payload); err != nil {
					t.Fatal(err)
				}
				if state == "delivering" {
					if rows, err := store.ClaimCompatibilityQueuedMessages(frame.ID); err != nil || len(rows) != 1 {
						t.Fatalf("delivery claim=%#v err=%v", rows, err)
					}
				}
			case "dispatch":
				failed := FrameStatusFailed
				if _, err := store.UpdateFrame(frame.ID, UpdateFrameInput{Status: &failed}); err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateAutoResumeDispatch(frame.ID, frame.ID, frame.ProjectID, frame.AgentName, "explicit_recovery"); err != nil {
					t.Fatal(err)
				}
			default:
				if _, err := store.UpdateFrame(frame.ID, UpdateFrameInput{Status: &state}); err != nil {
					t.Fatal(err)
				}
			}
			before, _, err := store.GetFrame(frame.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(frame, true, 7)); !errors.Is(err, ErrConversationArchiveBusy) {
				t.Fatalf("pending %s archive error=%v", state, err)
			}
			after, _, err := store.GetFrame(frame.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("archive changed pending work: before=%#v after=%#v err=%v", before, after, err)
			}
		})
	}
}

func TestConversationArchiveIncludesHiddenDescendantLiveWork(t *testing.T) {
	store, repo := newConversationArchiveFixture(t)
	root := conversationArchiveFixtureFrame(t, store, "archive-parent", 1, false, false)
	child, err := store.CreateFrame(CreateFrameInput{
		ID: "hidden-active-child", ProjectID: root.ProjectID, ParentFrameID: root.ID,
		AgentName: "agent", Status: FrameStatusProcessing, ConversationType: "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFrameSubmissionMetadata(child.ID, map[string]any{"kind": "aside"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "frame:" + child.ID, OwnerID: "archive-owner", ExternalID: child.ID, SessionID: child.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: child.ProjectID, RootFrameID: root.ID, FrameID: child.ID, Epoch: 1,
	}); err != nil {
		t.Fatal(err)
	}
	store.now = time.Now
	claim := archiveFixtureClaim(t, repo, child)
	if _, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(root, true, 7)); !errors.Is(err, ErrConversationArchiveBusy) {
		t.Fatalf("hidden descendant archive error=%v", err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(context.Background(), claim); err != nil {
		t.Fatalf("archive stopped descendant: %v", err)
	}
}

func TestConversationArchiveExplicitPauseCanRestoreWithoutRunning(t *testing.T) {
	store, repo := newConversationArchiveFixture(t)
	frame := conversationArchiveFixtureFrame(t, store, "archive-paused", 1, false, false)
	store.now = time.Now
	claim := archiveFixtureClaim(t, repo, frame)
	interrupted, err := repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: claim, ClientMessageID: "explicit-pause", ReasonCode: "user_requested_pause", Resumable: true, AutoResume: false,
	})
	if err != nil || !interrupted.Created {
		t.Fatalf("interrupt=%#v err=%v", interrupted, err)
	}
	result, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(frame, true, 7))
	if err != nil || !result.Changed {
		t.Fatalf("durably paused archive=%#v err=%v", result, err)
	}
	if claimed, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, RunnerID: "forbidden-archived-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence,
	}); !errors.Is(err, transcriptstore.ErrConversationArchived) || claimed.Claimed {
		t.Fatalf("archived checkpoint claimed=%#v err=%v", claimed, err)
	}
	before, err := repo.GetStream(context.Background(), claim.StreamUID, claim.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(frame, false, 8)); err != nil || !result.Changed {
		t.Fatalf("paused restore=%#v err=%v", result, err)
	}
	if candidate, err := repo.ClaimNextRunner(context.Background(), transcriptstore.ClaimNextRunnerInput{RunnerID: "ordinary-pool", TTL: time.Minute}); err != nil || candidate.Claimed {
		t.Fatalf("restore automatically resumed: %#v err=%v", candidate, err)
	}
	after, err := repo.GetStream(context.Background(), claim.StreamUID, claim.OwnerID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("restore admitted work: before=%#v after=%#v err=%v", before, after, err)
	}
}

func TestConversationArchivePauseWithPendingKernelApprovalIsStillBusy(t *testing.T) {
	store, repo, claim := newKernelLocalOperationFixture(t)
	operation := createKernelLocalOperationForTest(t, store, repo, claim, "archive-kernel-pending", "archive-kernel-call")
	if _, err := repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: claim, ClientMessageID: "pause-before-kernel-approval", ReasonCode: "user_requested_pause", Resumable: true, AutoResume: false,
	}); err != nil {
		t.Fatal(err)
	}
	frame, _, err := store.GetFrame(operation.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateConversationArchive(context.Background(), ConversationArchiveUpdate{
		OwnerID: claim.OwnerID, FrameID: frame.ID, IncarnationID: frame.IncarnationID, ExpectedRevision: 0, Archived: true,
	}); !errors.Is(err, ErrConversationArchiveBusy) {
		t.Fatalf("pending kernel permission hidden by pause: %v", err)
	}
}

func TestConversationArchiveComputeJobAdmissionSharesTheDurableBoundary(t *testing.T) {
	store, _ := newConversationArchiveFixture(t)
	frame := conversationArchiveFixtureFrame(t, store, "archive-compute", 1, false, false)
	store.now = time.Now
	if _, err := store.SetBYOCEnabled("modal", "archive-owner", true); err != nil {
		t.Fatal(err)
	}
	job := ComputeJob{JobID: "bound-remote-job", ProjectID: frame.ProjectID, FrameID: &frame.ID, RootFrameID: &frame.RootFrameID,
		Environment: "python", TierType: "remote", Provider: "byoc:modal"}
	if _, err := store.CreateComputeJob("archive-owner", job); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(frame, true, 7)); !errors.Is(err, ErrConversationArchiveBusy) {
		t.Fatalf("pending compute was hidden: %v", err)
	}
	if _, err := store.TransitionComputeJob("archive-owner", job.JobID, ComputeJobFailed, "cancelled_before_dispatch", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(frame, true, 7)); err != nil {
		t.Fatal(err)
	}
	job.JobID = "late-bound-job"
	if _, err := store.CreateComputeJob("archive-owner", job); !errors.Is(err, transcriptstore.ErrConversationArchived) {
		t.Fatalf("archived root accepted new compute: %v", err)
	}
	job.JobID, job.FrameID, job.RootFrameID = "project-only-job", nil, nil
	if _, err := store.CreateComputeJob("archive-owner", job); err != nil {
		t.Fatalf("unbound project job acquired an invented conversation scope: %v", err)
	}
}

func TestConversationArchiveLegacyActivationCannotChangeState(t *testing.T) {
	store, _ := newConversationArchiveFixture(t)
	frame := conversationArchiveFixtureFrame(t, store, "archive-legacy-activation", 1, false, true)
	before, _, err := store.GetFrame(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, activate := range []func(string) (bool, error){store.ActivateCompletedCompatibilityFrameRequest, store.ActivateCompatibilityFrameRequest} {
		if activated, err := activate(frame.ID); !errors.Is(err, transcriptstore.ErrConversationArchived) || activated {
			t.Fatalf("legacy activation=%t err=%v", activated, err)
		}
	}
	after, _, err := store.GetFrame(frame.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("archived legacy input mutated state: before=%#v after=%#v err=%v", before, after, err)
	}
}
