package server

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestManualContextCompactionArchiveYieldsToRealtimeBacklog(t *testing.T) {
	f, receipt := pendingManualArchiveForTest(t)
	for index := 0; index < transcriptWebCoordinatorDeliveryBatch*2+1; index++ {
		appendSkillControlInput(t, f, fmt.Sprintf("live-backlog-%d", index), "Synthetic visible delivery", false)
	}
	state, err := f.server.runTranscriptWebDeliveryPass(context.Background())
	if err != nil || !state.Immediate {
		t.Fatalf("fixture did not retain runnable realtime delivery: immediate=%t err=%v", state.Immediate, err)
	}
	current, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.ArchiveStatus != "pending" {
		t.Fatalf("archive slice delayed the runnable realtime backlog: %#v %v", current, err)
	}
}

func TestManualContextCompactionArchiveRepairsAfterNewInput(t *testing.T) {
	f, receipt := pendingManualArchiveForTest(t)
	appendSkillControlInput(t, f, "next-ordinary", "Continue this synthetic logical task", false)
	before, err := f.repo.GetStream(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if before.InputRevision <= receipt.InputRevision {
		t.Fatal("fixture did not submit a newer input")
	}
	f.server.repairContextCompactionArchivePass(context.Background(), time.Now().UTC())
	current, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.ArchiveStatus != "available" || current.InputRevision != receipt.InputRevision || current.TerminalEventID != receipt.TerminalEventID {
		t.Fatalf("newer input invalidated historical archive source: %#v %v", current, err)
	}
	after, err := f.repo.GetStream(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil || after.InputRevision != before.InputRevision || after.ConsumedInputRevision != before.ConsumedInputRevision {
		t.Fatalf("archive repair consumed or replaced the next input: before=%#v after=%#v err=%v", before, after, err)
	}
}

func TestManualContextCompactionArchiveNormalCommitWakesCoordinator(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	f.server.startTranscriptWebDelivery()
	request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
	if _, err := f.store.AdmitContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request); err != nil {
		t.Fatal(err)
	}
	result, err := f.server.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{
		SessionID: f.stream.FrameID, RunnerID: "archive-normal-producer", Model: "must-not-run", DisableMCPDiscovery: true,
		LeaseTTL: time.Minute, RequestTimeout: time.Second, PreparationTimeout: time.Second,
	})
	if err != nil || !result.Claimed || result.Status != "completed" {
		t.Fatalf("real manual-compaction runner result=%#v err=%v", result, err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		receipt, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.ArchiveStatus == "available" {
			if receipt.State != "committed" || receipt.TerminalEventID != result.FinishEventID {
				t.Fatalf("archive belongs to a different completion: %#v", receipt)
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("canonical commit did not wake original coordinator: %#v", receipt)
		case <-poll.C:
		}
	}
}
