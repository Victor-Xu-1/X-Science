package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestManualContextCompactionRunsWithoutProviderOrScientificGoal(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
	if _, err := f.store.AdmitContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer provider.Close()
	result, err := f.server.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{SessionID: f.stream.FrameID,
		RunnerID: "manual-compaction-test", Endpoint: provider.URL + "/v1/chat/completions", Model: "must-not-run",
		DisableMCPDiscovery: true, LeaseTTL: time.Minute, RequestTimeout: time.Second, PreparationTimeout: time.Second})
	if err != nil || !result.Claimed || result.Status != "completed" {
		t.Fatalf("real runner=%#v err=%v", result, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("maintenance invoked provider %d times", calls.Load())
	}
	receipt, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request)
	if err != nil || receipt.State != "committed" || receipt.TerminalEventID != result.FinishEventID || receipt.BoundaryEventID == 0 {
		t.Fatalf("canonical result=%#v err=%v", receipt, err)
	}
	var assistantCount int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND runner_attempt=? AND event_type='assistant_message'`,
		f.stream.UID, receipt.Attempt).Scan(&assistantCount); err != nil {
		t.Fatal(err)
	}
	if assistantCount != 0 {
		t.Fatal("maintenance generated a task answer")
	}
}

func TestManualContextCompactionN6BothReadPathsExcludeMaintenance(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	appendManualCompactionInputForTest(t, f)
	claimSkillControlInput(t, f)
	receipt := finishManualCompactionForTest(t, f)
	snapshot, err := f.repo.GetProjectionSnapshot(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"history", "latest-head"} {
		t.Run(scenario, func(t *testing.T) {
			var positions map[string]transcriptstore.NotificationSourcePosition
			if scenario == "latest-head" {
				positions = map[string]transcriptstore.NotificationSourcePosition{f.stream.UID: {
					Incarnation: f.identity.access.Frame.IncarnationID, Publication: snapshot.ThroughPublicationSequence}}
			}
			page, err := f.repo.ReadNotificationSources(context.Background(), f.stream.OwnerID, "", false, positions)
			if err != nil {
				t.Fatal(err)
			}
			ordinaryFound := false
			for _, source := range page.Sources {
				for _, terminal := range source.Terminals {
					if terminal.EventID == receipt.TerminalEventID || terminal.Purpose == transcriptstore.ContextCompactionPurpose {
						t.Fatal("maintenance generated human task notification")
					}
					if terminal.Attempt == 1 {
						ordinaryFound = true
					}
				}
			}
			if scenario == "history" && !ordinaryFound {
				t.Fatal("ordinary task completion disappeared")
			}
			if scenario == "latest-head" {
				for _, source := range page.Sources {
					if len(source.Terminals) != 0 {
						t.Fatalf("head emitted maintenance: %#v", source.Terminals)
					}
				}
			}
		})
	}
}

func TestManualContextCompactionArchivePendingDoesNotBlockNextInput(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	appendManualCompactionInputForTest(t, f)
	claimSkillControlInput(t, f)
	receipt := finishManualCompactionForTest(t, f)
	if _, err := f.db.Exec(`CREATE TRIGGER reject_manual_archive BEFORE INSERT ON compaction_archives
  BEGIN SELECT RAISE(ABORT,'injected archive failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.server.repairContextCompactionArchive(context.Background(), f.stream.OwnerID, &receipt)
	if receipt.State != "committed" || receipt.ArchiveStatus != "pending" {
		t.Fatalf("projection failure changed canonical result: %#v", receipt)
	}
	if err := f.server.preemptContextCompactionForInput(context.Background(), f.stream.OwnerID, f.stream.FrameID); err != nil {
		t.Fatal(err)
	}
	appendSkillControlInput(t, f, "after-archive-pending", "A new ordinary request.", false)
	if _, err := f.db.Exec("DROP TRIGGER reject_manual_archive"); err != nil {
		t.Fatal(err)
	}
	f.server.repairLatestContextCompactionArchive(f.stream.FrameID)
	current, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.State != "committed" || current.ArchiveStatus != "available" {
		t.Fatalf("historical repair=%#v err=%v", current, err)
	}
	f.server.repairLatestContextCompactionArchive(f.stream.FrameID)
	var count int
	if err = f.db.QueryRow("SELECT COUNT(*) FROM compaction_archives WHERE frame_id=? AND source_event_id=?", f.stream.FrameID, receipt.BoundaryEventID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("archive count=%d", count)
	}
}
