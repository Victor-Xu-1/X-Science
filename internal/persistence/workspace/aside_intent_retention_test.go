package workspace

import (
	"context"
	"testing"
	"time"
)

func TestAsideIntentLifecycleReceiptsSurviveOrdinaryTransportSweep(t *testing.T) {
	store, _, stream, _ := newTranscriptCancellationFixture(t)
	ctx := context.Background()
	accepted := CompatibilityAsideInput{ID: "retained-child", ParentRootFrameID: stream.FrameID, Request: "Retained accepted operation", IntentID: "retained-accepted"}
	if _, err := store.CreateCompatibilityAsideWithTranscript(ctx, accepted, "retained-accepted-input", nil); err != nil {
		t.Fatal(err)
	}
	cancelled := CompatibilityAsideInput{ParentRootFrameID: stream.FrameID, Request: "Cancelled before acceptance", IntentID: "retained-cancelled"}
	if _, err := store.CancelCompatibilityAsideIntent(ctx, stream.OwnerID, cancelled); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ordinary-drained", "fake-aside-marker"} {
		dedupe := map[string]any{"request": "ordinary"}
		if id == "fake-aside-marker" {
			dedupe["_aside_contract"] = 1
		}
		if _, err := store.RecordCompatibilityDeliveredIntent(stream.FrameID, id, dedupe, map[string]any{"frame_id": stream.FrameID}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := store.QueueCompatibilityMessage(stream.FrameID, "ordinary-retracted", map[string]any{"request": "retract"}, map[string]any{"text": "retract"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetractCompatibilityQueuedMessage(stream.FrameID, "ordinary-retracted"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE queued_user_messages SET resolved_at=? WHERE state IN ('drained','retracted')`, time.Now().UTC().Add(-9*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	report, err := store.SweepDataLifecycle(ctx, DefaultDataLifecyclePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if report.ResolvedQueuedIntents != 3 {
		t.Fatalf("ordinary retention changed: %#v", report)
	}
	for _, id := range []string{accepted.IntentID, cancelled.IntentID} {
		if _, found, err := store.GetCompatibilityAsideAcceptance(ctx, stream.OwnerID, stream.FrameID, id); err != nil || !found {
			t.Fatalf("lifecycle receipt lost: %s %t %v", id, found, err)
		}
	}
	if _, err := store.db.Exec(`DELETE FROM frames WHERE id=?`, accepted.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.GetCompatibilityMessageIntent(accepted.IntentID); err != nil || found {
		t.Fatalf("explicit Frame deletion FK changed: %t %v", found, err)
	}
}

func TestAsideIntentLifecycleSweepRechecksReadCandidates(t *testing.T) {
	store, _, stream, _ := newTranscriptCancellationFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	cutoff := now.Add(-7 * 24 * time.Hour)
	ids := []string{"unchanged", "changed-payload", "requeued", "recently-resolved"}
	for _, id := range ids {
		if _, err := store.RecordCompatibilityDeliveredIntent(stream.FrameID, id, map[string]any{"request": id}, map[string]any{"frame_id": stream.FrameID}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec(`UPDATE queued_user_messages SET resolved_at=?`, now.Add(-9*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Classification happens before the write transaction; changes in that gap
	// must survive the final compare-and-delete, including newly queued input.
	// Holding the sole writer connection also proves classification uses the
	// existing read pool, not a long scan through the write connection.
	writer, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	candidates, err := resolvedQueuedIntentCandidates(readCtx, store.readDatabase(), cutoff)
	cancel()
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || len(candidates) != len(ids) {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	if _, err := store.db.Exec(`UPDATE queued_user_messages SET payload=payload||' ' WHERE intent_id='changed-payload'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE queued_user_messages SET state='queued',resolved_at=NULL WHERE intent_id='requeued'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE queued_user_messages SET resolved_at=? WHERE intent_id='recently-resolved'`, now); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.QueueCompatibilityMessage(stream.FrameID, "new-queued", map[string]any{"request": "new"}, map[string]any{"text": "new"}); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	deleted, err := sweepResolvedQueuedIntents(ctx, tx, cutoff, candidates)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, id := range append(ids, "new-queued") {
		_, found, err := store.GetCompatibilityMessageIntent(id)
		if err != nil || found != (id != "unchanged") {
			t.Fatalf("intent %q found=%t err=%v", id, found, err)
		}
	}
}
