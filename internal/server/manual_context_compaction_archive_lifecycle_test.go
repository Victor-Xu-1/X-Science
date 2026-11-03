package server

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func pendingManualArchiveForTest(t *testing.T) (*agentSaveArtifactsFixture, transcriptstore.ContextCompactionReceipt) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	appendManualCompactionInputForTest(t, f)
	claimSkillControlInput(t, f)
	receipt := finishManualCompactionForTest(t, f)
	if receipt.State != "committed" || receipt.ArchiveStatus != "pending" {
		t.Fatalf("fixture did not retain an unprojected canonical boundary: %#v", receipt)
	}
	return f, receipt
}

func TestManualContextCompactionArchiveNotificationRetry(t *testing.T) {
	f, receipt := pendingManualArchiveForTest(t)
	if _, err := f.db.Exec(`CREATE TRIGGER reject_archive_ready BEFORE INSERT ON realtime_events
 WHEN NEW.event_type='compaction_status' BEGIN SELECT RAISE(ABORT,'injected notification failure'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if retry := f.server.repairContextCompactionArchivePass(context.Background(), now); retry != contextCompactionArchiveRetry {
		t.Fatalf("notification failure retry=%v", retry)
	}
	current, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.ArchiveStatus != "available" {
		t.Fatalf("archive did not commit before the notification failure: %#v %v", current, err)
	}
	if _, err := f.db.Exec("DROP TRIGGER reject_archive_ready"); err != nil {
		t.Fatal(err)
	}
	for pass := 1; pass <= 2; pass++ {
		f.server.repairContextCompactionArchivePass(context.Background(), now.Add(time.Duration(pass)*contextCompactionArchiveRetry))
	}
	id := "context-compaction-archive:" + receipt.Request.RequestID + ":" + strconv.FormatInt(receipt.BoundaryEventID, 10)
	event, found, err := f.store.GetRealtimeEventByID(id)
	if err != nil || !found || event.UserID != f.stream.OwnerID || event.ProjectID != f.stream.ProjectID || event.FrameID != f.stream.FrameID ||
		event.Payload["request_id"] != receipt.Request.RequestID || event.Payload["state"] != "committed" {
		t.Fatalf("archive-ready notification did not recover its exact target: event=%#v found=%t err=%v", event, found, err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM compaction_archives WHERE frame_id=? AND source_event_id=?`, f.stream.FrameID, receipt.BoundaryEventID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("notification retry recreated archive: count=%d err=%v", count, err)
	}
}

func TestManualContextCompactionArchiveIdleWakeAndCancellation(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	now := time.Now().UTC()
	if delay := f.server.repairContextCompactionArchivePass(context.Background(), now); delay != 0 {
		t.Fatalf("empty scan armed an idle timer: %v", delay)
	}
	if delay := f.server.repairContextCompactionArchivePass(context.Background(), now.Add(100*time.Millisecond)); delay != 0 {
		t.Fatalf("unrelated wake inside idle cooldown invented pending work: %v", delay)
	}
	appendManualCompactionInputForTest(t, f)
	claimSkillControlInput(t, f)
	receipt := finishManualCompactionForTest(t, f)
	f.server.signalContextCompactionArchiveRecovery(receipt)
	if delay := f.server.repairContextCompactionArchivePass(context.Background(), now.Add(100*time.Millisecond)); delay != 900*time.Millisecond {
		t.Fatalf("a commit wake inside idle cooldown was lost or spun: %v", delay)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if delay := f.server.repairContextCompactionArchivePass(ctx, now.Add(contextCompactionArchivePace)); delay != 0 {
		t.Fatalf("cancelled coordinator continued maintenance: %v", delay)
	}
	current, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.ArchiveStatus != "pending" {
		t.Fatalf("cancelled/background-throttled slice mutated archive: %#v %v", current, err)
	}
	f.server.repairContextCompactionArchivePass(context.Background(), now.Add(contextCompactionArchivePace))
	current, err = f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.ArchiveStatus != "available" {
		t.Fatalf("due existing-coordinator slice lost the committed boundary: %#v %v", current, err)
	}
}

func TestManualContextCompactionArchiveRecoveredByExistingCoordinator(t *testing.T) {
	f, receipt := pendingManualArchiveForTest(t)
	// The execution owner is absent, as after a restart between canonical
	// commit and archive projection. No compatibility/archive GET is issued.
	for pass := 0; pass < 2; pass++ {
		if err := f.server.runTranscriptWebDeliveryCycle(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	current, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.State != "committed" || current.ArchiveStatus != "available" || current.TerminalEventID != receipt.TerminalEventID {
		t.Fatalf("existing startup/commit coordinator did not project its canonical archive: receipt=%#v err=%v", current, err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM compaction_archives WHERE frame_id=? AND source_event_id=?`,
		f.stream.FrameID, receipt.BoundaryEventID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("idempotent recovery count=%d err=%v", count, err)
	}
	response := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+f.stream.FrameID+"/context-compactions", nil, f.stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot HTTP %d: %s", response.Code, response.Body.String())
	}
	operation := p3DecodeObject(t, response)["operation"].(map[string]any)
	if operation["archive_status"] != "available" || operation["can_submit_next_input"] != true || operation["execution_observation"] != "unavailable" {
		t.Fatalf("readback invented physical execution status or retained a gate: %#v", operation)
	}
}

func TestManualContextCompactionArchiveProjectionFailureRemainsRecoverable(t *testing.T) {
	f, receipt := pendingManualArchiveForTest(t)
	if _, err := f.db.Exec(`CREATE TRIGGER reject_lifecycle_archive BEFORE INSERT ON compaction_archives
 BEGIN SELECT RAISE(ABORT,'injected projection failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.server.runTranscriptWebDeliveryCycle(context.Background()); err != nil {
		t.Fatalf("derived archive failure blocked realtime delivery: %v", err)
	}
	current, err := f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.State != "committed" || current.ArchiveStatus != "pending" || !current.CanSubmitNextInput {
		t.Fatalf("projection failure changed canonical completion/admission: receipt=%#v err=%v", current, err)
	}
	if _, err := f.db.Exec("DROP TRIGGER reject_lifecycle_archive"); err != nil {
		t.Fatal(err)
	}
	// An existing coordinator cycle can finish the same projection after its
	// storage condition recovers; it must not execute or finish the input again.
	// Advance only this synthetic coordinator's clock past its real retry
	// budget; do not sleep or loosen the production scheduler.
	f.server.contextCompactionArchives.Lock()
	f.server.contextCompactionArchives.next = time.Time{}
	f.server.contextCompactionArchives.Unlock()
	if err := f.server.runTranscriptWebDeliveryCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err = f.repo.ReadContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, receipt.Request)
	if err != nil || current.ArchiveStatus != "available" || current.Attempt != receipt.Attempt || current.TerminalEventID != receipt.TerminalEventID {
		t.Fatalf("recovered projection did not preserve original execution: receipt=%#v err=%v", current, err)
	}
	var attempts int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_attempts WHERE stream_uid=? AND claimed_input_revision=?`,
		f.stream.UID, receipt.InputRevision).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("projection recovery replayed maintenance: attempts=%d err=%v", attempts, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.server.preemptContextCompactionForInput(ctx, f.stream.OwnerID, f.stream.FrameID); err != nil {
		t.Fatalf("recovered archive retained an input gate: %v", err)
	}
}
