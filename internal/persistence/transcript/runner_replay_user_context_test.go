package transcript

import (
	"context"
	"fmt"
	"testing"
)

func TestRunnerReplayRetainsUserConstraintsAcrossRepeatedCompaction(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "retained-user-context", "owner-a")
	var inputs []int64
	for round := 0; round < 3; round++ {
		input, _, err := repo.AppendUserEvent(context.Background(), AppendUserEventInput{
			StreamUID: claim.StreamUID, OwnerID: claim.OwnerID,
			ClientMessageID: fmt.Sprintf("constraint-%d", round),
			PayloadJSON:     []byte(fmt.Sprintf(`{"text":"preserve original constraint %d"}`, round)),
		})
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input.EventID)
		appendRunnerReplayCheckpoint(t, repo, claim, fmt.Sprintf("compact-%d", round),
			`{"status":"completed","toolPhase":"auto_compact","summary":"recent operations only"}`)
		for i := 0; i < 8; i++ {
			appendRunnerReplayCheckpoint(t, repo, claim, fmt.Sprintf("operation-%d-%d", round, i), `{"status":"running"}`)
		}
		events, err := repo.ListRunnerReplay(context.Background(), ListRunnerReplayInput{
			StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, MessageLimit: 1, CheckpointLimit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range inputs {
			if !runnerReplayContainsEvent(events, id) {
				t.Fatalf("compaction %d lost original user event %d", round, id)
			}
		}
	}
}
