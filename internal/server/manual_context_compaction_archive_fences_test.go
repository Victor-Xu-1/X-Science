package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func secondManualArchiveForTest(t *testing.T, f *agentSaveArtifactsFixture) transcriptstore.ContextCompactionReceipt {
	t.Helper()
	frame, err := f.store.CreateFrame(workspace.CreateFrameInput{ID: "frame-z", ProjectID: f.stream.ProjectID,
		AgentName: "OPERON", Status: "processing", ConversationType: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	access, found, err := f.store.GetKernelFrameAccessContext(context.Background(), frame.ID)
	if err != nil || !found {
		t.Fatalf("second frame access found=%t err=%v", found, err)
	}
	stream, err := f.repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{UID: "frame:" + frame.ID,
		OwnerID: f.stream.OwnerID, ExternalID: frame.ID, SessionID: frame.ID, Kind: transcriptstore.StreamKindFrameRef,
		ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	copy := *f
	copy.stream, copy.identity = stream, &agentKernelContext{access: access, workspaceDir: f.projectPath}
	appendSkillControlInput(t, &copy, "second-initial", "Synthetic second-frame work", false)
	claimSkillControlInput(t, &copy)
	finishSkillControlInput(t, &copy)
	request := typedManualCompactionRequestForTest(t, &copy, "9b79accf-67f1-455a-81f6-019e113516d1")
	if _, err := f.store.AdmitContextCompaction(context.Background(), stream.OwnerID, stream.FrameID, request); err != nil {
		t.Fatal(err)
	}
	claimSkillControlInput(t, &copy)
	return finishManualCompactionForTest(t, &copy)
}

func TestManualContextCompactionArchivePoisonDoesNotStarveAnotherFrame(t *testing.T) {
	f, poison := pendingManualArchiveForTest(t)
	healthy := secondManualArchiveForTest(t, f)
	if _, err := f.db.Exec(`CREATE TRIGGER reject_one_archive BEFORE INSERT ON compaction_archives
 WHEN NEW.frame_id='frame-save' BEGIN SELECT RAISE(ABORT,'injected poison archive'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if retry := f.server.repairContextCompactionArchivePass(context.Background(), now); retry != contextCompactionArchiveRetry {
		t.Fatalf("poison did not back off: %v", retry)
	}
	if retry := f.server.repairContextCompactionArchivePass(context.Background(), now.Add(time.Millisecond)); retry <= 0 {
		t.Fatal("rapid realtime wake bypassed repair backoff")
	}
	f.server.repairContextCompactionArchivePass(context.Background(), now.Add(contextCompactionArchiveRetry))
	current, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, healthy.FrameID, healthy.Request)
	if err != nil || current.ArchiveStatus != "available" {
		t.Fatalf("poison starved later owned frame: %#v %v", current, err)
	}
	current, err = f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, poison.FrameID, poison.Request)
	if err != nil || current.ArchiveStatus != "pending" || !current.CanSubmitNextInput {
		t.Fatalf("poison was cleared or blocked input: %#v %v", current, err)
	}
}

func TestManualContextCompactionArchiveRejectsStaleOrForeignScope(t *testing.T) {
	for _, scenario := range []string{"retired-frame", "changed-project-owner", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			f, receipt := pendingManualArchiveForTest(t)
			statement := `UPDATE frames SET incarnation_id='retired-frame' WHERE id='frame-save'`
			if scenario == "changed-project-owner" {
				statement = `UPDATE projects SET user_id='foreign-owner' WHERE id='project-save'`
			}
			if scenario == "cancelled" {
				// A cancelled, never-committed maintenance is not a source boundary.
				f = newAgentSaveArtifactsFixture(t)
				finishSkillControlInput(t, f)
				request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
				if _, err := f.store.AdmitContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.CancelContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			f.server.repairContextCompactionArchivePass(context.Background(), time.Now().UTC())
			var count int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM compaction_archives WHERE frame_id=? AND source_event_id=?`,
				f.stream.FrameID, receipt.BoundaryEventID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("foreign/stale/cancelled source gained an archive: count=%d err=%v", count, err)
			}
		})
	}
}

func TestManualContextCompactionArchiveRepresentativeHistoryFitsSlice(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	payload, err := json.Marshal(map[string]any{"status": "completed", "detail": strings.Repeat("Observed synthetic checkpoint evidence. ", 16)})
	if err != nil {
		t.Fatal(err)
	}
	const events = 6000
	for index := 0; index < events; index++ {
		if index%500 == 0 {
			if _, err := f.repo.HeartbeatRunner(ctx, transcriptstore.HeartbeatRunnerInput{Claim: f.claim, TTL: time.Minute}); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, _, err := f.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim,
			ClientMessageID: fmt.Sprintf("representative-%d", index), Phase: transcriptstore.RunnerPhaseExecuting,
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
	receipt := finishManualCompactionForTest(t, f)
	started := time.Now()
	f.server.repairContextCompactionArchivePass(ctx, started)
	elapsed := time.Since(started)
	current, err := f.repo.ReadContextCompaction(ctx, f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.ArchiveStatus != "available" {
		t.Fatalf("representative %d-event archive never fit its bounded repair slice: elapsed=%v receipt=%#v err=%v", events, elapsed, current, err)
	}
	var messages int
	if err := f.db.QueryRow(`SELECT message_count FROM compaction_archives WHERE frame_id=? AND source_event_id=?`,
		f.stream.FrameID, receipt.BoundaryEventID).Scan(&messages); err != nil || messages < events {
		t.Fatalf("bounded archive silently shortened history: messages=%d err=%v", messages, err)
	}
	t.Logf("representative canonical checkpoints=%d payload_bytes=%d retained_messages=%d repair_elapsed=%s budget=%s", events, len(payload)*events, messages, elapsed, contextCompactionArchiveSlice)
}
