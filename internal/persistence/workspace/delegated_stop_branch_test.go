package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func delegatedStartedInvocation(t *testing.T) (*Store, *transcriptstore.Repository, transcriptstore.RunnerClaim, transcriptstore.Stream) {
	t.Helper()
	store, repo, claim := newToolCallBatchFixture(t)
	batch, items := createToolCallBatchForTest(t, store, repo, claim, "delegate-source",
		toolCallBatchTestCall{id: "outer-call", name: "read_file", arguments: map[string]any{"path": "evidence.txt"}})
	var err error
	batch, err = store.ClaimToolCallBatch(t.Context(), ClaimToolCallBatchInput{Claim: claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion})
	if err != nil {
		t.Fatal(err)
	}
	appendToolCallBatchStart(t, store, repo, claim, batch, items[0], "delegate-source-start")
	stream, err := repo.GetStream(t.Context(), claim.StreamUID, claim.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	return store, repo, claim, stream
}

func TestDelegatedBranchStopFencesOldAdmissionButAllowsNewExplicitCall(t *testing.T) {
	store, repo, claim, parent := delegatedStartedInvocation(t)
	requests := []KernelDelegateRequest{{Task: "Frozen child task"}}
	old, err := store.CaptureKernelDelegationAdmission(t.Context(), parent.FrameID, parent.OwnerID, "old-call", "outer-call", claim, requests)
	if err != nil {
		t.Fatal(err)
	}
	stop := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "branch", "")
	if _, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, stop); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "old-call", Requests: requests, Admission: old}); !errors.Is(err, ErrDelegatedStopTargetStale) {
		t.Fatalf("old admission=%v", err)
	}
	replayedCapture, err := store.CaptureKernelDelegationAdmission(t.Context(), parent.FrameID, parent.OwnerID, "old-call", "outer-call", claim, requests)
	if err != nil || replayedCapture.ID != old.ID {
		t.Fatalf("capture replay=%#v %v", replayedCapture, err)
	}
	if _, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "old-call", Requests: requests, Admission: replayedCapture}); !errors.Is(err, ErrDelegatedStopTargetStale) {
		t.Fatalf("capture replay bypass=%v", err)
	}
	current, err := store.CaptureKernelDelegationAdmission(t.Context(), parent.FrameID, parent.OwnerID, "new-call", "outer-call", claim, requests)
	if err != nil {
		t.Fatal(err)
	}
	children, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "new-call", Requests: requests, Admission: current})
	if err != nil || len(children) != 1 {
		t.Fatalf("explicit new admission=%#v %v", children, err)
	}
	if _, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, stop); err != nil {
		t.Fatal(err)
	}
	child, found, err := store.GetKernelSupervisedChild(t.Context(), parent.FrameID, children[0].FrameID, parent.OwnerID)
	if err != nil || !found || child.Status != "processing" {
		t.Fatalf("old stop replay touched new child=%#v %v", child, err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(t.Context(), claim); err != nil {
		t.Fatalf("branch Stop touched parent: %v", err)
	}
	newStop := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "branch", "")
	receipt, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, newStop)
	if err != nil || len(receipt.Targets) != 1 || receipt.Targets[0].Target.FrameID != child.FrameID {
		t.Fatalf("verified branch targets=%#v %v", receipt, err)
	}
}

func TestDelegatedStopUnavailableOriginCannotBypassSnapshot(t *testing.T) {
	store, repo, parent, _ := newTranscriptCancellationFixture(t)
	children, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "origin-test", Requests: []KernelDelegateRequest{{Task: "Task"}}})
	if err != nil {
		t.Fatal(err)
	}
	request := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "child", children[0].FrameID)
	// Simulate a retained record whose original parent incarnation is gone.
	// The public generic writer below is separately proved unable to forge it.
	err = repo.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
		id := delegatedRecordID("origin", children[0].FrameID, request.Target.FrameIncarnationID)
		var record delegationOriginRecord
		raw, _, err := readDelegatedRecord(t.Context(), tx, children[0].FrameID, id, delegationOriginEventType, &record)
		if err != nil {
			return err
		}
		record.AdmissionID, record.Scope.ParentFrameIncarnationID = "retired-admission", "retired-incarnation"
		return writeDelegatedRecord(t.Context(), tx, children[0].FrameID, id, delegationOriginEventType, raw, record, store.now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.DelegatedStopSnapshot(t.Context(), parent.OwnerID, parent.FrameID, "")
	if err != nil || len(snapshot.Targets) != 1 || snapshot.Targets[0].CanStop {
		t.Fatalf("invalid source capability=%#v %v", snapshot, err)
	}
	if _, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, request); !errors.Is(err, ErrDelegatedStopOriginUnavailable) {
		t.Fatalf("disabled source POST=%v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=?`, request.Target.StreamUID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("disabled target cancelled count=%d %v", count, err)
	}
}

func TestDelegatedControlFactsCannotBeGenericOrCloned(t *testing.T) {
	store, _, parent, _ := newTranscriptCancellationFixture(t)
	for _, kind := range []string{delegationAdmissionEventType, delegationOriginEventType, delegationFenceEventType, DelegatedStopReceiptEventType} {
		if _, err := store.AppendFrameEvent(FrameEventInput{FrameID: parent.FrameID, Type: kind, Payload: map[string]any{"version": 1}}); !errors.Is(err, ErrDelegatedStopInvalid) {
			t.Fatalf("generic %s=%v", kind, err)
		}
	}
	request := delegatedStopRequestForTest(t, store, parent.FrameID, parent.OwnerID, "branch", "")
	if _, err := store.AdmitDelegatedStop(t.Context(), parent.OwnerID, request); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	events, err := branchSourceEvents(t.Context(), tx, parent.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if reservedDelegatedControlEvent(event.Type) {
			t.Fatalf("clone retained authority=%s", event.Type)
		}
	}
	raw, _ := json.Marshal(request)
	if len(raw) == 0 {
		t.Fatal("empty frozen request")
	}
}
