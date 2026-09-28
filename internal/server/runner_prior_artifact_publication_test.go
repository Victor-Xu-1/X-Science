package server

import (
	"context"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestPriorArtifactRelationPublicationCompletesAndReplaysWithRealSQLite(t *testing.T) {
	ctx := context.Background()
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "publication-project", "publication-frame")
	app := &Server{workspaceStore: store, transcriptStore: repo}
	submit := func(id string) {
		t.Helper()
		if _, _, err := app.submitFrameMessage(store, frameMessageSubmission{
			FrameID: "publication-frame", MessageUUID: "message-" + id, ClientMessageID: "client-" + id, Text: "Continue the evidence report.",
		}); err != nil {
			t.Fatal(err)
		}
	}
	submit("first")
	stream, found, err := repo.GetFrameStreamBySession(ctx, "local", "publication-frame")
	if err != nil || !found {
		t.Fatalf("stream: %v %v", found, err)
	}
	claim := func() transcriptstore.RunnerClaim {
		t.Helper()
		result, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "publication-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
		})
		if err != nil || !result.Claimed {
			t.Fatalf("claim: %#v %v", result, err)
		}
		return result.Claim
	}
	commit := func(claim transcriptstore.RunnerClaim, id string, artifact transcriptWebReadModelArtifactFixture, relations ...transcriptstore.ArtifactRelation) {
		t.Helper()
		_, source, _, err := repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
			Claim: claim, ClientMessageID: id, Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true,
			PayloadJSON: []byte(`{"toolName":"save_artifacts","toolPhase":"start"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		for ordinal, relation := range relations {
			if _, err := db.Exec(`INSERT INTO transcript_artifact_commits(
				stream_uid,runner_attempt,source_event_id,ordinal,artifact_id,version_id,relation,created_at
			) VALUES(?,?,?,?,?,?,?,?)`, stream.UID, claim.Attempt, source.EventID, ordinal,
				artifact.artifactID, artifact.versionID, string(relation), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
	}
	prior := seedTranscriptWebReadModelArtifact(t, store, db, "publication-project", "publication-frame", "prior-input", "input.txt", []byte("original observations"), "runner")
	first := claim()
	commit(first, "input-used", prior, transcriptstore.ArtifactRelationConsumed)
	commit(first, "input-saved", prior, transcriptstore.ArtifactRelationProduced)
	firstFinish, _, _, err := repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: first, ClientMessageID: "finish-first", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	submit("second")
	second := claim()
	if second.Attempt != first.Attempt+1 {
		t.Fatalf("expected a new turn, got %#v", second)
	}
	current := seedTranscriptWebReadModelArtifact(t, store, db, "publication-project", "publication-frame", "current-output", "summary.txt", []byte("new analysis"), "runner")
	commit(second, "output-saved", current, transcriptstore.ArtifactRelationProduced)
	authority := &transcriptRunnerAuthority{Stream: stream, Claim: second}
	payload := map[string]any{"text": "The summary uses [the prior input]({{artifact:" + prior.versionID + "}})."}
	for replay := 0; replay < 2; replay++ {
		if err := app.appendTranscriptAssistantEvent(ctx, authority, "final", payload); err != nil {
			t.Fatalf("publishing/replaying citation to consumed and produced input failed: %v", err)
		}
	}
	var assistantID int64
	var events, refs, cited, produced int
	if err := db.QueryRow(`SELECT COUNT(*),MIN(event_id) FROM transcript_events
		WHERE stream_uid=? AND runner_attempt=? AND event_type='assistant_message'`, stream.UID, second.Attempt).Scan(&events, &assistantID); err != nil || events != 1 {
		t.Fatalf("idempotent final event: %d %v", events, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*),SUM(relation='cited'),SUM(relation='produced') FROM transcript_artifact_refs
		WHERE stream_uid=? AND source_event_id=?`, stream.UID, assistantID).Scan(&refs, &cited, &produced); err != nil || refs != 2 || cited != 1 || produced != 1 {
		t.Fatalf("public references: total=%d cited=%d produced=%d error=%v", refs, cited, produced, err)
	}
	var preserved, currentBound int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_artifact_commits WHERE stream_uid=? AND runner_attempt=? AND bound_event_id=?`, stream.UID, first.Attempt, firstFinish.EventID).Scan(&preserved); err != nil || preserved != 2 {
		t.Fatalf("prior audit relations changed: %d %v", preserved, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_artifact_commits WHERE stream_uid=? AND runner_attempt=? AND bound_event_id=?`, stream.UID, second.Attempt, assistantID).Scan(&currentBound); err != nil || currentBound != 1 {
		t.Fatalf("current output did not bind to final answer: %d %v", currentBound, err)
	}
	if _, _, _, err := repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: second, ClientMessageID: "finish-second", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	state, err := repo.GetRunnerRuntimeState(ctx, stream.UID, stream.OwnerID, second.Attempt)
	if err != nil || state.Status != "completed" {
		t.Fatalf("final settlement failed: %#v %v", state, err)
	}
}
