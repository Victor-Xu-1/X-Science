package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestWebConversationHostSnapshotReplayKeepsOriginalBranchAfterSwitch(t *testing.T) {
	f := newCanonicalHostInputFixture(t)
	path := "/api/conversations/" + f.frame.ID + "/messages"
	first := p3JSONRequest(t, f.server, http.MethodPost, path, f.body, f.stream.OwnerID)
	if first.Code != http.StatusAccepted {
		t.Fatalf("send=%d %s", first.Code, first.Body.String())
	}
	original := p3DecodeObject(t, first)
	originalReceipt := original["input_receipt"].(map[string]any)
	// Branch edits require the prior input to have been consumed. Exercise a
	// genuine completed input, not an impossible fork over pending admission.
	claim, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "before-branch-switch", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	if _, _, _, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: claim.Claim, ClientMessageID: "before-branch-switch-done", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	state, err := f.repo.GetBranchState(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	forked, err := f.repo.ForkFrameUserMessageBranch(context.Background(), transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: state.ActiveBranchID,
		ExpectedActiveBranchID: state.ActiveBranchID, ExpectedGeneration: state.Generation,
		ClientMutationID: "newer-active-branch", SourceClientMessageID: original["msg_id"].(string),
		// The seed runner_finished projects an assistant row at index 1.
		SourceMessageIndex: 2, ReplacementText: "Use a different branch", Destinations: []string{"ws"},
	})
	if err != nil || !forked.Created {
		t.Fatalf("fork=%#v err=%v", forked, err)
	}
	before := f.counts(t)
	if _, err := f.server.revokeHostGrant(f.stream.OwnerID, f.root); err != nil {
		t.Fatal(err)
	}
	retry := p3JSONRequest(t, f.server, http.MethodPost, path, f.body, f.stream.OwnerID)
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry=%d %s", retry.Code, retry.Body.String())
	}
	response := p3DecodeObject(t, retry)
	receipt := response["input_receipt"].(map[string]any)
	if response["msg_id"] != original["msg_id"] || receipt["branch_id"] != originalReceipt["branch_id"] ||
		receipt["branch_generation"] != originalReceipt["branch_generation"] || receipt["replayed"] != true {
		t.Fatalf("old receipt was rebound to the new branch: %#v", response)
	}
	after, err := f.repo.GetBranchState(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil || after.ActiveBranchID != forked.BranchID || after.Generation != forked.Generation || f.counts(t) != before {
		t.Fatalf("historical retry mutated the active branch/dispatch: %#v err=%v", after, err)
	}
}

func TestWebConversationHostSnapshotReplayRejectsChangedBodyBeforeMutableValidation(t *testing.T) {
	f := newCanonicalHostInputFixture(t)
	path := "/api/conversations/" + f.frame.ID + "/messages"
	first := p3JSONRequest(t, f.server, http.MethodPost, path, f.body, f.stream.OwnerID)
	if first.Code != http.StatusAccepted {
		t.Fatalf("send=%d %s", first.Code, first.Body.String())
	}
	if _, err := f.server.revokeHostGrant(f.stream.OwnerID, f.root); err != nil {
		t.Fatal(err)
	}
	before := f.counts(t)
	for _, field := range []string{"content", "files", "session_options"} {
		t.Run(field, func(t *testing.T) {
			changed := copyMapAny(f.body)
			switch field {
			case "content":
				changed[field] = "A changed request"
			case "files":
				changed[field] = []string{"/missing/other-input"}
			case "session_options":
				changed[field] = map[string]any{"memory_mode": "on"}
			}
			response := p3JSONRequest(t, f.server, http.MethodPost, path, changed, f.stream.OwnerID)
			if response.Code != http.StatusConflict || p3DecodeObject(t, response)["code"] != "MESSAGE_INTENT_CONFLICT" {
				t.Fatalf("changed original request accepted/revalidated: %d %s", response.Code, response.Body.String())
			}
		})
	}
	if after := f.counts(t); after != before {
		t.Fatalf("conflicts changed execution: %v %v", before, after)
	}
}
