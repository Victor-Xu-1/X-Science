package workspace

import (
	"context"
	"reflect"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestConversationArchiveOrdinaryAndBranchInputAreRejectedAtomically(t *testing.T) {
	for _, selectedBranch := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "selected-branch"}[selectedBranch], func(t *testing.T) {
			store, repo := newConversationArchiveFixture(t)
			frame := conversationArchiveFixtureFrame(t, store, "archived-root", 1, false, true)
			stream, err := repo.GetStream(context.Background(), "frame:"+frame.ID, "archive-owner")
			if err != nil {
				t.Fatal(err)
			}
			branch, err := repo.GetBranchState(context.Background(), stream.UID, stream.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			before, _, err := store.GetFrame(frame.ID)
			if err != nil {
				t.Fatal(err)
			}
			input := transcriptstore.AppendFrameUserEventInput{
				StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "archive-input",
				FrameEventID: "archive-frame-input", MessageUUID: "archive-input", Text: "Do not run from an archived conversation",
				Destinations: []string{"ws"},
			}
			if selectedBranch {
				input.ClientMessageID = "branch-continue:archive-input"
				input.MessageUUID = input.ClientMessageID
				input.FrameEventID = "frame-branch-continue:" + frame.ID + ":" + branch.ActiveBranchID + ":archive-input"
				result, err := repo.AppendFrameUserEventToBranch(context.Background(), transcriptstore.AppendFrameUserEventToBranchInput{
					AppendFrameUserEventInput: input, TargetBranchID: branch.ActiveBranchID,
					ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation, ClientMutationID: "archive-input",
				})
				if err == nil || result.Created || result.Switched {
					t.Errorf("archived branch admitted result=%#v err=%v", result, err)
				}
			} else {
				if _, _, created, err := repo.AppendFrameUserEvent(context.Background(), input); err == nil || created {
					t.Errorf("archived ordinary input admitted created=%t err=%v", created, err)
				}
			}
			afterStream, err := repo.GetStream(context.Background(), stream.UID, stream.OwnerID)
			if err != nil || !reflect.DeepEqual(stream, afterStream) {
				t.Errorf("rejected admission changed stream: before=%#v after=%#v err=%v", stream, afterStream, err)
			}
			afterBranch, err := repo.GetBranchState(context.Background(), stream.UID, stream.OwnerID)
			if err != nil || !reflect.DeepEqual(branch, afterBranch) {
				t.Errorf("rejected admission changed branch: before=%#v after=%#v err=%v", branch, afterBranch, err)
			}
			after, _, err := store.GetFrame(frame.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Errorf("rejected admission activated frame: before=%#v after=%#v err=%v", before, after, err)
			}
			for _, table := range []string{"transcript_events", "transcript_delivery_intents", "transcript_runner_attempts"} {
				var count int
				if err := store.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE stream_uid=?", stream.UID).Scan(&count); err != nil || count != 0 {
					t.Errorf("rejected admission left %s rows=%d err=%v", table, count, err)
				}
			}
		})
	}
}
