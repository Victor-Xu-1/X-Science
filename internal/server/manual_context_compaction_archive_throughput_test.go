package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualContextCompactionArchiveLargeSourceArtifactProjection(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	payload, err := json.Marshal(map[string]any{"status": "completed", "detail": strings.Repeat("s", 2167)})
	if err != nil {
		t.Fatal(err)
	}
	appendArchiveThroughputArtifactsForTest(t, f, 682)
	// The ordinary input, claim checkpoint, three artifact messages, terminal,
	// and these checkpoints make 6019 canonical events and about 13.25 MB. Content
	// is synthetic; no production task fields or scientific data are copied.
	for index := 0; index < 6013; index++ {
		if index%500 == 0 {
			if _, err := f.repo.HeartbeatRunner(ctx, transcriptstore.HeartbeatRunnerInput{Claim: f.claim, TTL: time.Minute}); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, _, err := f.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim,
			ClientMessageID: fmt.Sprintf("large-source-%d", index), Phase: transcriptstore.RunnerPhaseExecuting,
			Resumable: true, PayloadJSON: payload}); err != nil {
			t.Fatal(err)
		}
	}
	finishSkillControlInput(t, f)
	request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
	if _, err := f.store.AdmitContextCompaction(ctx, f.stream.OwnerID, f.stream.FrameID, request); err != nil {
		t.Fatal(err)
	}
	f.stream, err = f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	claimSkillControlInput(t, f)
	committed := finishManualCompactionForTest(t, f)
	appendSkillControlInput(t, f, "later-running-large-source", "Continue the synthetic task.", false)
	claimSkillControlInput(t, f)
	readModel, err := f.store.TranscriptWebReadModel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.server.transcriptWebReadModel = readModel
	var count, bytes int
	if err := f.db.QueryRow(`SELECT count(*),SUM(length(payload_json)) FROM transcript_events
 WHERE stream_uid=? AND publication_seq<=?`, f.stream.UID, request.Expected.ThroughPublicationSeq).Scan(&count, &bytes); err != nil {
		t.Fatal(err)
	}
	if count != 6019 || bytes < 13_000_000 || bytes > 13_500_000 {
		t.Fatalf("fixture shape events=%d bytes=%d", count, bytes)
	}
	var refs, versions int
	if err := f.db.QueryRow(`SELECT count(*),count(DISTINCT ref.version_id)
 FROM transcript_branch_events member JOIN transcript_events event ON event.stream_uid=member.stream_uid AND event.event_id=member.event_id
 JOIN transcript_artifact_refs ref ON ref.stream_uid=event.stream_uid AND ref.runner_attempt=event.runner_attempt
 WHERE member.stream_uid=? AND member.branch_id=? AND event.publication_seq<=?
 AND (ref.source_event_id=event.event_id OR event.event_type IN ('runner_finished','runner_reclaimed'))`,
		f.stream.UID, request.Expected.BranchID, request.Expected.ThroughPublicationSeq).Scan(&refs, &versions); err != nil || refs != 1364 || versions != 682 {
		t.Fatalf("fixture projected refs=%d versions=%d err=%v", refs, versions, err)
	}
	t.Logf("artifact projection shape refs=%d versions=%d", refs, versions)
	started := time.Now()
	state, cycleErr := f.server.runTranscriptWebDeliveryPass(ctx)
	coordinatorElapsed := time.Since(started)
	receipt, readErr := f.repo.ReadContextCompaction(ctx, f.stream.OwnerID, f.stream.FrameID, committed.Request)
	originalAvailable := cycleErr == nil && readErr == nil && receipt.ArchiveStatus == "available"
	t.Logf("original_coordinator events=%d bytes=%d elapsed=%s budget=%s immediate=%t available=%t cycle_error_type=%T read_error_type=%T",
		count, bytes, coordinatorElapsed, contextCompactionArchiveSlice, state.Immediate, originalAvailable, cycleErr, readErr)

	// Diagnostic stage timings retain the result of the real 750 ms pass above.
	// A separate bounded context in this isolated fixture identifies the hot
	// original stage; it does not increase the production repair budget.
	diagnostic, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	started = time.Now()
	boundary, generation, err := f.repo.ContextCompactionBoundary(diagnostic, f.stream.OwnerID, f.stream.FrameID, committed.Request)
	t.Logf("stage=canonical_boundary elapsed=%s error_type=%T", time.Since(started), err)
	if err != nil {
		t.Fatal(err)
	}
	readRequest := committed.Request
	readRequest.Expected.BranchGeneration = generation
	started = time.Now()
	entries, err := f.server.contextCompactionSource(diagnostic, readRequest, f.stream.OwnerID)
	t.Logf("stage=frozen_source elapsed=%s error_type=%T", time.Since(started), err)
	if err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	var message eventjournal.Message
	if err := json.Unmarshal(boundary.PayloadJSON, &message); err != nil {
		t.Fatal(err)
	}
	messages := compactionMessagesAfterLastBoundary(entries)
	tokens := estimateCompactionMessageTokens(messages)
	t.Logf("stage=materialize_and_estimate elapsed=%s retained=%d", time.Since(started), len(messages))
	started = time.Now()
	archive, err := f.store.CreateCompactionArchive(workspace.CreateCompactionArchiveInput{Context: diagnostic,
		ID: "context-compaction-archive:" + committed.Request.RequestID, FrameID: f.stream.FrameID,
		MessageCount: len(messages), TokenCount: &tokens, Summary: compactSummaryFromMessage(message), Messages: messages,
		SourceEventID: &boundary.EventID, CreatedAt: boundary.CreatedAt,
		CanonicalSource: &workspace.ContextCompactionArchiveSource{OwnerID: f.stream.OwnerID, Request: committed.Request}})
	t.Logf("stage=canonical_archive_write elapsed=%s error_type=%T", time.Since(started), err)
	if err != nil {
		t.Fatal(err)
	}
	if archive.MessageCount != len(messages) || len(messages) != 6017 {
		t.Fatalf("archive lost frozen history: retained=%d archive=%d", len(messages), archive.MessageCount)
	}
	var archiveCount int
	if err := f.db.QueryRow(`SELECT count(*) FROM compaction_archives WHERE frame_id=? AND source_event_id=?`,
		f.stream.FrameID, committed.BoundaryEventID).Scan(&archiveCount); err != nil || archiveCount != 1 {
		t.Fatalf("replay was not idempotent: archives=%d err=%v", archiveCount, err)
	}
	if !originalAvailable {
		t.Fatal("original coordinator did not complete a valid bounded archive; diagnostic write does not turn the original pass green")
	}
}
