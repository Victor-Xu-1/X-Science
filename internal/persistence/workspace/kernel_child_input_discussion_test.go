package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestKernelChildDiscussionIsTypedAtomicAndNeverAnswersLaterPrompt(t *testing.T) {
	store, repo, parent, _ := newTranscriptCancellationFixture(t)
	children, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{
		ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "discussion-child",
		Requests: []KernelDelegateRequest{{Task: "Ask before proceeding"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	child := children[0]
	stream, found, err := repo.GetFrameStreamBySession(t.Context(), parent.OwnerID, child.FrameID)
	if err != nil || !found {
		t.Fatalf("stream found=%t err=%v", found, err)
	}
	claimed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: parent.OwnerID, RunnerID: "question-owner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	park := func(claim transcriptstore.RunnerClaim, id string) transcriptstore.AskUserOriginV1 {
		t.Helper()
		_, _, err := store.ParkAskUserWithTranscript(t.Context(), ParkAskUserInput{
			FrameID: child.FrameID, ToolID: id, ToolName: "AskUserQuestion",
			Questions: []any{map[string]any{"question": "Choose a route", "header": "Route", "options": []any{
				map[string]any{"label": "A", "description": "Route A"},
				map[string]any{"label": "B", "description": "Route B"},
			}}},
		}, transcriptstore.AppendRunnerCheckpointInput{Claim: claim, ClientMessageID: "pause-" + id,
			Phase: transcriptstore.RunnerPhaseWaitingUser, Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_user_response"}`)})
		if err != nil {
			t.Fatal(err)
		}
		metadata, found, err := store.GetFrameRuntimeMetadata(child.FrameID)
		if err != nil || !found {
			t.Fatalf("pending metadata found=%t err=%v", found, err)
		}
		origins, _ := metadata.ContextData[askUserTranscriptOriginsKey].(map[string]any)
		encoded, err := json.Marshal(origins[id])
		if err != nil {
			t.Fatal(err)
		}
		origin, err := transcriptstore.DecodeAskUserOriginV1(encoded)
		if err != nil {
			t.Fatal(err)
		}
		return origin
	}
	firstOrigin := park(claimed.Claim, "question-1")
	queued, err := store.QueueKernelSupervisionMessageWithID(t.Context(), parent.FrameID, child.FrameID, parent.OwnerID, "parent-discussion", "allow", "question")
	if err != nil || queued.Queued == nil {
		t.Fatalf("queue=%#v err=%v", queued, err)
	}
	note, err := store.QueueKernelSupervisionMessageWithID(t.Context(), parent.FrameID, child.FrameID, parent.OwnerID, "additional-note", "Keep the evidence", "info")
	if err != nil || note.Queued == nil {
		t.Fatalf("note=%#v err=%v", note, err)
	}
	target, _, err := store.KernelChildDispatchTarget(t.Context(), parent.OwnerID, parent.FrameID, child.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := store.AdmitKernelChildMessageInputs(t.Context(), child, target, []KernelChildQueuedMessage{*queued.Queued, *note.Queued}); err != nil {
			t.Fatal(err)
		}
	}
	terminal, found, err := repo.FindFrameAskUserTerminalResult(t.Context(), transcriptstore.FindFrameAskUserTerminalResultInput{
		OwnerID: parent.OwnerID, FrameID: child.FrameID, StreamUID: stream.UID, BranchID: firstOrigin.BranchID, BranchGeneration: firstOrigin.BranchGeneration, ToolUseID: "question-1",
	})
	if err != nil || !found || terminal.Origin != firstOrigin || terminal.Result.Action != transcriptstore.AskUserActionDiscuss || terminal.Result.Status != transcriptstore.AskUserStatusDiscussed || len(terminal.Result.Answers) != 0 || terminal.Result.Message != "allow" || !strings.Contains(terminal.ModelContinuation, "not a human answer") {
		t.Fatalf("parent text gained human authority: %#v found=%t err=%v", terminal, found, err)
	}
	current, err := repo.GetStream(t.Context(), stream.UID, parent.OwnerID)
	if err != nil || current.InputRevision != 3 {
		t.Fatalf("input duplicated: %#v err=%v", current, err)
	}
	metadata, found, err := store.GetFrameRuntimeMetadata(child.FrameID)
	if err != nil || !found || len(compatibilityPendingInputRequests(metadata.ContextData)) != 0 {
		t.Fatalf("pending not resolved: %#v found=%t err=%v", metadata, found, err)
	}
	var grants, dispatches int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM approval_policy_grants`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE frame_id=? AND event_type='frame_resumed'`, child.FrameID).Scan(&dispatches); err != nil {
		t.Fatal(err)
	}
	if grants != 0 || dispatches != 0 {
		t.Fatalf("discussion created competing authority: grants=%d dispatches=%d", grants, dispatches)
	}
	resumed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: parent.OwnerID, RunnerID: "question-owner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !resumed.Claimed {
		t.Fatalf("resume=%#v err=%v", resumed, err)
	}
	secondOrigin := park(resumed.Claim, "question-2")
	if err := store.AdmitKernelChildMessageInputs(t.Context(), child, target, []KernelChildQueuedMessage{*queued.Queued}); err != nil {
		t.Fatal(err)
	}
	_, found, err = repo.FindFrameAskUserTerminalResult(t.Context(), transcriptstore.FindFrameAskUserTerminalResultInput{
		OwnerID: parent.OwnerID, FrameID: child.FrameID, StreamUID: stream.UID, BranchID: secondOrigin.BranchID, BranchGeneration: secondOrigin.BranchGeneration, ToolUseID: "question-2",
	})
	if err != nil || found {
		t.Fatalf("old queue resolved new question: found=%t err=%v", found, err)
	}
	metadata, found, err = store.GetFrameRuntimeMetadata(child.FrameID)
	if err != nil || !found || len(compatibilityPendingInputRequests(metadata.ContextData)) != 1 {
		t.Fatalf("new pending lost: %#v found=%t err=%v", metadata, found, err)
	}

	// Corrupt/mixed pending metadata never turns agent discussion into a
	// permission decision. It must leave both the original prompt and input.
	metadata.ContextData["_pending_access_request"] = map[string]any{"tool_id": "approval-1", "kind": "permission"}
	if _, err := store.SetFrameRuntimeMetadata(child.FrameID, metadata); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(metadata.ContextData)
	second, err := store.QueueKernelSupervisionMessageWithID(t.Context(), parent.FrameID, child.FrameID, parent.OwnerID, "mixed-parent-discussion", "approve", "question")
	if err != nil || second.Queued == nil {
		t.Fatalf("second queue=%#v err=%v", second, err)
	}
	if err := store.AdmitKernelChildMessageInputs(t.Context(), child, target, []KernelChildQueuedMessage{*second.Queued}); err == nil {
		t.Fatal("mixed pending was accepted")
	}
	after, _, err := store.GetFrameRuntimeMetadata(child.FrameID)
	encoded, _ := json.Marshal(after.ContextData)
	if err != nil || string(before) != string(encoded) {
		t.Fatalf("rejected discussion mutated pending: %v", err)
	}
	current, err = repo.GetStream(t.Context(), stream.UID, parent.OwnerID)
	if err != nil || current.InputRevision != 3 {
		t.Fatalf("rejected discussion admitted input: %#v err=%v", current, err)
	}
}
