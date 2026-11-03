package server

import (
	"context"
	"fmt"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualContextCompactionArchiveCoordinatorWithLaterRunningInput(t *testing.T) {
	f, committed := pendingManualArchiveForTest(t)
	ctx := context.Background()
	readModel, err := f.store.TranscriptWebReadModel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.server.transcriptWebReadModel = readModel
	appendSkillControlInput(t, f, "later-running-input", "Continue the same synthetic logical task.", false)
	claimSkillControlInput(t, f)
	if f.claim.ClaimedInputRevision <= committed.InputRevision {
		t.Fatal("fixture did not claim a later ordinary input")
	}
	work, err := f.repo.NextPendingContextCompactionArchive(ctx, true, "", 0)
	if err != nil || work == nil || work.BoundaryEventID != committed.BoundaryEventID || work.Request != committed.Request {
		t.Fatalf("committed source was not selected after later input: work=%#v err=%v", work, err)
	}
	for pass := 0; pass < 4; pass++ {
		state, err := f.server.runTranscriptWebDeliveryPass(ctx)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := f.repo.ReadContextCompaction(ctx, f.stream.OwnerID, f.stream.FrameID, committed.Request)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("coordinator pass=%d immediate=%t retry=%s archive=%s", pass, state.Immediate, state.RetryAfter, receipt.ArchiveStatus)
		if receipt.ArchiveStatus == "available" {
			if receipt.Attempt != committed.Attempt || receipt.TerminalEventID != committed.TerminalEventID {
				t.Fatal("archive repair substituted a later execution receipt")
			}
			var running int
			if err := f.db.QueryRow(`SELECT count(*) FROM transcript_runner_attempts WHERE stream_uid=? AND attempt=? AND status='running'`, f.stream.UID, f.claim.Attempt).Scan(&running); err != nil || running != 1 {
				t.Fatalf("archive repair changed the later running input: count=%d err=%v", running, err)
			}
			return
		}
	}
	t.Fatal("original coordinator left a valid committed source pending after later input")
}

func TestManualContextCompactionArchiveNotStarvedByBackgroundProjectionProgress(t *testing.T) {
	f, committed := pendingManualArchiveForTest(t)
	ctx := context.Background()
	readModel, err := f.store.TranscriptWebReadModel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.server.transcriptWebReadModel = readModel
	appendSkillControlInput(t, f, "later-running-input", "Continue the same synthetic logical task.", false)
	claimSkillControlInput(t, f)
	for pass := 0; pass < 3; pass++ {
		// More than one background projection advances per coordinator lifetime,
		// while each original pass intentionally advances only one. These source
		// events have no realtime delivery destinations.
		for index := 0; index < 2; index++ {
			appendArchiveBackgroundProjectionForTest(t, f, fmt.Sprintf("background-%d-%d", pass, index))
		}
		state, err := f.server.runTranscriptWebDeliveryPass(ctx)
		if err != nil {
			t.Fatal(err)
		}
		progress, err := f.server.transcriptWebReadModelProgress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		runnable, err := f.server.hasRunnableTranscriptDeliveries(ctx, transcriptWebDestination)
		if err != nil || runnable || !state.Immediate || !progress.pending {
			t.Fatalf("fixture did not isolate progressing background work: runnable=%t immediate=%t pending=%t err=%v", runnable, state.Immediate, progress.pending, err)
		}
		receipt, err := f.repo.ReadContextCompaction(ctx, f.stream.OwnerID, f.stream.FrameID, committed.Request)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("background pass=%d immediate=%t realtime_runnable=%t archive=%s", pass, state.Immediate, runnable, receipt.ArchiveStatus)
		if receipt.ArchiveStatus == "available" {
			return
		}
	}
	t.Fatal("progressing background projections starved a committed archive despite caught-up realtime delivery")
}

func appendArchiveBackgroundProjectionForTest(t *testing.T, f *agentSaveArtifactsFixture, frameID string) {
	t.Helper()
	frame, err := f.store.CreateFrame(workspace.CreateFrameInput{ID: frameID, ProjectID: f.stream.ProjectID,
		AgentName: "OPERON", Status: "processing", ConversationType: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := f.repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "frame:" + frame.ID, OwnerID: f.stream.OwnerID, ExternalID: frame.ID, SessionID: frame.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: frameID + "-source",
		FrameEventID: frameID + "-event", MessageUUID: frameID + "-message", Text: "Synthetic background history."}); err != nil || !created {
		t.Fatalf("append background source created=%t err=%v", created, err)
	}
}
