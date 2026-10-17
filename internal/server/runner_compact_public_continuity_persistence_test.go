package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
)

func TestRunnerCompactPublicHandoffSurvivesCheckpointResumeAndSecondCompaction(t *testing.T) {
	fixture := newCompactionEpisodePersistenceFixture(t)
	ctx := t.Context()
	loadReplay := func() []eventjournal.Entry {
		t.Helper()
		entries, err := fixture.server.loadTranscriptRunnerReplay(ctx, fixture.run.Transcript, 100, 100)
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}
	appendPublic := func(identity, text string) int64 {
		t.Helper()
		if err := fixture.server.appendTranscriptAssistantEvent(ctx, fixture.run.Transcript, identity, map[string]any{
			"text": text, "role": "assistant",
		}); err != nil {
			t.Fatal(err)
		}
		clientID := transcriptRunnerClientMessageID(fixture.run.Transcript.Claim, identity)
		for _, entry := range loadReplay() {
			if entry.SourceEventType == "assistant_message" && entry.ClientMessageID == clientID &&
				runnerMessageText(entry.Message) == text {
				return entry.EventID
			}
		}
		t.Fatal("canonical replay omitted the newly appended public assistant message")
		return 0
	}
	requirePruned := func(entries []eventjournal.Entry, eventIDs ...int64) {
		t.Helper()
		for _, entry := range entries {
			for _, eventID := range eventIDs {
				if entry.EventID == eventID {
					t.Fatalf("canonical compact replay retained raw public source event %d", eventID)
				}
			}
		}
	}

	const priorPublic = "PUBLIC_PRE_C1_OPEN: the comparison remains uncertain; the alternative explanation still needs review."
	const currentPublic = "PUBLIC_POST_RESUME_NEW: a new comparison is available, but the earlier open explanation is not yet resolved."
	priorPublicID := appendPublic("public-handoff-before-c1", priorPublic)
	batchID := fixture.completeTool(t, agentruntime.ToolCall{
		ID: "public-handoff-receipt", Name: "web_fetch", Arguments: json.RawMessage(`{}`),
	}, `{"ok":true,"observation":"TYPED_COMPACT_RECEIPT"}`)
	before := loadReplay()
	receipt, _ := requireCompactionEpisodeOutcome(t, before, batchID)
	expectedLedger, err := sessionRunnerToolContinuityRecords(before)
	if err != nil || len(expectedLedger) != 1 || expectedLedger[0].ToolCallID != "public-handoff-receipt" ||
		!expectedLedger[0].Successful {
		t.Fatalf("canonical typed receipt ledger=%+v err=%v", expectedLedger, err)
	}
	var rootID int64
	var rootText string
	for _, entry := range before {
		if runnerEntryStartsNewLogicalTask(entry) {
			if rootID != 0 || entry.SourceEventType != "user_message" {
				t.Fatal("fixture did not retain exactly one canonical root user task")
			}
			rootID, rootText = entry.EventID, runnerMessageText(entry.Message)
		}
	}
	if rootID <= 0 || rootText == "" || strings.Contains(rootText, priorPublic) {
		t.Fatal("fixture root task is unavailable or duplicates the public observation")
	}
	var originalReceipt string
	if err := fixture.db.QueryRowContext(ctx, `SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`,
		fixture.run.Transcript.Stream.UID, receipt.EventID).Scan(&originalReceipt); err != nil {
		t.Fatal(err)
	}

	first, compactOne, err := fixture.server.autoCompactSessionForRunner(ctx, fixture.options, fixture.session, before, fixture.run)
	if err != nil || !compactOne.Triggered {
		t.Fatalf("first compact triggered=%t err=%v", compactOne.Triggered, err)
	}
	firstSummary, firstID, found := latestCompactModelContext(first)
	if !found || firstID <= priorPublicID || !strings.Contains(firstSummary, priorPublic) {
		t.Fatal("first compact did not capture the original public observation")
	}
	requirePruned(first, priorPublicID)
	archives, err := fixture.store.ListCompactionArchives(fixture.session.ID)
	if err != nil || len(archives) != 1 || archives[0].SourceEventID == nil || *archives[0].SourceEventID != firstID {
		t.Fatalf("first compact archive count=%d err=%v", len(archives), err)
	}

	priorClaim := fixture.run.Transcript.Claim
	fixture.resume(t, "runtime_draining")
	resumedClaim := fixture.run.Transcript.Claim
	if resumedClaim.ClaimToken == priorClaim.ClaimToken || resumedClaim.Attempt != priorClaim.Attempt ||
		resumedClaim.ClaimedInputRevision != priorClaim.ClaimedInputRevision {
		t.Fatal("normal checkpoint reclaim did not preserve the task while rotating the lease")
	}
	currentPublicID := appendPublic("public-handoff-after-resume", currentPublic)
	resumed := loadReplay()
	requirePruned(resumed, priorPublicID)
	if currentPublicID <= firstID || len(resumed) == 0 || !runnerEntryStartsNewLogicalTask(resumed[0]) || resumed[0].EventID != rootID {
		t.Fatal("resumed public progress did not remain on the original canonical task")
	}

	second, compactTwo, err := fixture.server.autoCompactSessionForRunner(ctx, fixture.options, fixture.session, resumed, fixture.run)
	if err != nil || !compactTwo.Triggered {
		t.Fatalf("second compact triggered=%t err=%v", compactTwo.Triggered, err)
	}
	_, secondID, found := latestCompactModelContext(second)
	if !found || secondID <= firstID {
		t.Fatalf("second compact reused the first boundary: first=%d second=%d", firstID, secondID)
	}
	var compactCount int
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_events
		WHERE stream_uid=? AND event_type='runner_checkpoint' AND json_extract(payload_json,'$.toolPhase')='auto_compact'`,
		fixture.run.Transcript.Stream.UID).Scan(&compactCount); err != nil || compactCount != 2 {
		t.Fatalf("actual compact checkpoints=%d err=%v", compactCount, err)
	}
	archives, err = fixture.store.ListCompactionArchives(fixture.session.ID)
	if err != nil || len(archives) != 2 || archives[0].ID == archives[1].ID ||
		archives[0].SourceEventID == nil || *archives[0].SourceEventID != firstID ||
		archives[1].SourceEventID == nil || *archives[1].SourceEventID != secondID {
		t.Fatalf("distinct compact archives count=%d err=%v", len(archives), err)
	}

	// Reload through the real SQLite selection after C2. Neither original
	// assistant event may accidentally satisfy the public continuity assertion.
	after := loadReplay()
	requirePruned(after, priorPublicID, currentPublicID, firstID)
	actualLedger, err := sessionRunnerToolContinuityRecords(after)
	if err != nil || !reflect.DeepEqual(actualLedger, expectedLedger) {
		t.Fatalf("public continuity changed independent typed receipts: got=%+v want=%+v err=%v", actualLedger, expectedLedger, err)
	}
	var retainedReceipt string
	if err := fixture.db.QueryRowContext(ctx, `SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`,
		fixture.run.Transcript.Stream.UID, receipt.EventID).Scan(&retainedReceipt); err != nil || retainedReceipt != originalReceipt {
		t.Fatalf("immutable source receipt changed across compaction: %v", err)
	}
	messages, err := sessionEntriesToProviderMessages("controlled public handoff continuity", after)
	if err != nil {
		t.Fatal(err)
	}
	rootCopies := 0
	var providerText strings.Builder
	for _, message := range messages {
		if message.Role == "user" && message.Content == rootText {
			rootCopies++
		}
		providerText.WriteString(message.Content)
		providerText.WriteByte('\n')
	}
	if rootCopies != 1 {
		t.Fatalf("provider replay must retain the original user task verbatim once, got %d", rootCopies)
	}
	if strings.Count(providerText.String(), currentPublic) != 1 {
		t.Fatal("provider replay lost or duplicated the new public progress")
	}
	if strings.Count(providerText.String(), priorPublic) != 1 {
		t.Fatal("second compact lost the still-useful prior public observation after checkpoint resume")
	}
}
