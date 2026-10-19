package transcript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func TestRunnerReplayRetainsClosedHistoricalBatchAcrossGeneration(t *testing.T) {
	for _, state := range []string{"settled", "cancelled", "outcome_unknown"} {
		t.Run(state, func(t *testing.T) {
			repo, db, claim, root, terminals := historicalReplayFixture(t, state)
			input := ListRunnerReplayInput{StreamUID: claim.StreamUID, OwnerID: claim.OwnerID,
				MessageLimit: 1, CheckpointLimit: 1, MaxExpandedEvents: 16, MaxExpandedBytes: 1 << 20}
			events, err := repo.ListRunnerReplay(context.Background(), input)
			if err != nil || !runnerReplayContainsEvent(events, root.EventID) {
				t.Fatalf("historical assistant root was cropped: root=%d events=%d err=%v", root.EventID, len(events), err)
			}
			seen := map[int64]bool{}
			for _, event := range events {
				if seen[event.Event.EventID] {
					t.Fatalf("duplicate replay event %d", event.Event.EventID)
				}
				seen[event.Event.EventID] = true
			}
			for ordinal, terminal := range terminals {
				want := RunnerReplayToolOutcome{BatchID: "historical-batch", SourceEventID: root.EventID,
					Ordinal: int64(ordinal), ToolCallID: fmt.Sprintf("call-%d", ordinal), TerminalEventID: terminal.EventID}
				if outcome := runnerReplayToolOutcomeForEvent(events, terminal.EventID); outcome == nil || *outcome != want {
					t.Fatalf("historical terminal lost its exact relation: got=%+v want=%+v", outcome, want)
				}
				var stored []byte
				if err := db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`,
					claim.StreamUID, terminal.EventID).Scan(&stored); err != nil || string(stored) != string(terminal.PayloadJSON) {
					t.Fatalf("readback changed immutable terminal bytes: err=%v", err)
				}
			}
			var generation int64
			var storedState string
			if err := db.QueryRow(`SELECT branch_generation,state FROM transcript_tool_call_batches WHERE batch_id='historical-batch'`).
				Scan(&generation, &storedState); err != nil || generation != 1 || storedState != state {
				t.Fatalf("readback rebound historical execution authority: generation=%d state=%s err=%v", generation, storedState, err)
			}
		})
	}
}

func TestRunnerReplayHistoricalBatchPreservesReadFences(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*testing.T, *sql.DB, RunnerClaim, Event, []Event, *ListRunnerReplayInput)
		wantError error
		noClosure bool
	}{
		{"torn-closed-items", func(t *testing.T, db *sql.DB, _ RunnerClaim, _ Event, _ []Event, _ *ListRunnerReplayInput) {
			mustHistoricalReplayExec(t, db, `UPDATE transcript_tool_call_items SET terminal_event_id=NULL WHERE batch_id='historical-batch' AND ordinal=0`)
		}, ErrOpenToolBatch, false},
		{"terminal-outside-branch", func(t *testing.T, db *sql.DB, _ RunnerClaim, _ Event, _ []Event, _ *ListRunnerReplayInput) {
			// Point the malformed legacy relation at a nonmember; membership itself stays immutable.
			mustHistoricalReplayExec(t, db, `UPDATE transcript_tool_call_items SET terminal_event_id=1048576 WHERE batch_id='historical-batch' AND ordinal=0`)
		}, ErrOpenToolBatch, false},
		{"source-outside-branch", func(t *testing.T, db *sql.DB, _ RunnerClaim, _ Event, _ []Event, _ *ListRunnerReplayInput) {
			mustHistoricalReplayExec(t, db, `UPDATE transcript_tool_call_batches SET source_event_id=1048576 WHERE batch_id='historical-batch'`)
		}, nil, true},
		{"other-branch", func(t *testing.T, db *sql.DB, _ RunnerClaim, _ Event, _ []Event, _ *ListRunnerReplayInput) {
			mustHistoricalReplayExec(t, db, `UPDATE transcript_tool_call_batches SET branch_id='br_other' WHERE batch_id='historical-batch'`)
		}, nil, true},
		{"future-generation", func(t *testing.T, db *sql.DB, _ RunnerClaim, _ Event, _ []Event, _ *ListRunnerReplayInput) {
			mustHistoricalReplayExec(t, db, `UPDATE transcript_tool_call_batches SET branch_generation=3 WHERE batch_id='historical-batch'`)
		}, nil, true},
		{"older-open-batch", func(t *testing.T, db *sql.DB, _ RunnerClaim, _ Event, _ []Event, _ *ListRunnerReplayInput) {
			mustHistoricalReplayExec(t, db, `UPDATE transcript_tool_call_batches SET state='running' WHERE batch_id='historical-batch'`)
		}, nil, true},
		{"foreign-owner", func(_ *testing.T, _ *sql.DB, _ RunnerClaim, _ Event, _ []Event, input *ListRunnerReplayInput) {
			input.OwnerID = "other-owner"
		}, ErrOwnerMismatch, false},
		{"expanded-event-budget", func(_ *testing.T, _ *sql.DB, _ RunnerClaim, _ Event, _ []Event, input *ListRunnerReplayInput) {
			input.MaxExpandedEvents = 2
		}, ErrProviderReplayWindowTooLarge, false},
		{"expanded-byte-budget", func(_ *testing.T, _ *sql.DB, _ RunnerClaim, _ Event, _ []Event, input *ListRunnerReplayInput) {
			input.MaxExpandedBytes = 1
		}, ErrProviderReplayWindowTooLarge, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, claim, root, terminals := historicalReplayFixture(t, "settled")
			input := ListRunnerReplayInput{StreamUID: claim.StreamUID, OwnerID: claim.OwnerID,
				MessageLimit: 1, CheckpointLimit: 1, MaxExpandedEvents: 16, MaxExpandedBytes: 1 << 20}
			test.change(t, db, claim, root, terminals, &input)
			events, err := repo.ListRunnerReplay(context.Background(), input)
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("error=%v want=%v", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.noClosure {
				if runnerReplayContainsEvent(events, root.EventID) {
					t.Fatal("ineligible historical batch acquired source closure")
				}
				for _, event := range events {
					if event.ToolOutcome != nil {
						t.Fatalf("ineligible historical batch acquired canonical provenance: %+v", event.ToolOutcome)
					}
				}
			}
		})
	}
}

// The existing minimal batch fixture deliberately permits malformed relations
// for negative read tests. The server consumer test uses the real workspace
// schema and public continuation API instead.
func historicalReplayFixture(t *testing.T, state string) (*Repository, *sql.DB, RunnerClaim, Event, []Event) {
	t.Helper()
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "stream-historical-replay", "owner-a")
	installRunnerReplayToolBatchTables(t, db)
	root, terminals := seedRunnerReplayToolBatch(t, repo, db, claim, "historical-batch", "settled", 2)
	mustHistoricalReplayExec(t, db, `UPDATE transcript_tool_call_batches SET state=? WHERE batch_id='historical-batch'`, state)
	mustHistoricalReplayExec(t, db, `UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=?`, claim.StreamUID)
	return repo, db, claim, root, terminals
}

func mustHistoricalReplayExec(t *testing.T, db *sql.DB, query string, arguments ...any) {
	t.Helper()
	if _, err := db.Exec(query, arguments...); err != nil {
		t.Fatal(err)
	}
}
