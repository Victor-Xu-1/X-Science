package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestProjectedEventsToolOutcomeRequiresUniqueCanonicalRelation(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "stream-page-tool-origin", "owner-a")
	forged := appendRunnerReplayCheckpoint(t, repo, claim, "payload-origin-lookalike",
		`{"status":"completed","toolCallId":"call-0","toolPhase":"completed","ToolOutcome":{"BatchID":"forged","SourceEventID":1,"Ordinal":0,"ToolCallID":"call-0","TerminalEventID":3}}`)
	installRunnerReplayToolBatchTables(t, db)
	source, terminals := seedRunnerReplayToolBatch(t, repo, db, claim, "page-origin-batch", "settled", 2)
	replay, err := repo.ListRunnerReplay(context.Background(), ListRunnerReplayInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, MessageLimit: 10, CheckpointLimit: 10,
	})
	if err != nil {
		t.Fatalf("canonical fixture replay: %v", err)
	}
	input := ListProjectedEventsInput{StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, Limit: 100}
	plain, err := repo.ListProjectedEvents(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range plain {
		if event.ToolOutcome != nil {
			t.Fatal("default page acquired optional execution provenance")
		}
	}
	input.IncludeToolOutcomes = true
	projected, err := repo.ListProjectedEvents(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	assertProjectedToolOutcomePageUnchanged(t, plain, projected)
	for ordinal, terminal := range terminals {
		want := RunnerReplayToolOutcome{BatchID: "page-origin-batch", SourceEventID: source.EventID,
			Ordinal: int64(ordinal), ToolCallID: fmt.Sprintf("call-%d", ordinal), TerminalEventID: terminal.EventID}
		if outcome := runnerReplayToolOutcomeForEvent(replay, terminal.EventID); outcome == nil || *outcome != want {
			t.Fatalf("fixture lacks established Replay provenance: got=%+v want=%+v", outcome, want)
		}
		assertProjectedToolOutcome(t, projected, terminal.EventID, &want)
	}
	assertProjectedToolOutcome(t, projected, forged.EventID, nil)
	coordinates, err := repo.ListProjectedCoordinateEvents(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	assertProjectedToolOutcomePageUnchanged(t, projected, coordinates)
	assertProjectedToolOutcome(t, coordinates, terminals[0].EventID, projectedToolOutcomeForEvent(t, projected, terminals[0].EventID))
	encodedInput, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var publicInput map[string]json.RawMessage
	if err := json.Unmarshal(encodedInput, &publicInput); err != nil {
		t.Fatal(err)
	}
	if _, leaked := publicInput["IncludeToolOutcomes"]; leaked {
		t.Fatalf("private projection opt-in was serialized: %s", encodedInput)
	}

	// Both origins precede this terminal. Even a syntactically valid relation
	// cannot win by MIN or by the reusable tool-call ID when ownership is torn.
	shadow := appendRunnerReplayToolBatchRoot(t, repo, claim, "page-shadow-batch", "settled", []runnerReplayTestCall{{
		ID: "call-0", Name: "read_file", Arguments: map[string]any{"path": "other-notes.txt"},
	}})
	shared := appendRunnerReplayCheckpoint(t, repo, claim, "shared-terminal",
		`{"status":"completed","toolCallId":"call-0","toolPhase":"completed","toolResult":{"ok":true}}`)
	if source.EventID >= shared.EventID || shadow.EventID >= shared.EventID {
		t.Fatal("ambiguous fixture does not contain two valid earlier sources")
	}
	setRunnerReplayToolBatchTerminals(t, db, "page-origin-batch", []Event{shared})
	setRunnerReplayToolBatchTerminals(t, db, "page-shadow-batch", []Event{shared})
	input.IncludeToolOutcomes = false
	plain, err = repo.ListProjectedEvents(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.IncludeToolOutcomes = true
	ambiguous, err := repo.ListProjectedEvents(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	assertProjectedToolOutcomePageUnchanged(t, plain, ambiguous)
	assertProjectedToolOutcome(t, ambiguous, shared.EventID, nil)
	assertProjectedToolOutcome(t, ambiguous, forged.EventID, nil)
}

func TestProjectedEventsToolOutcomeRespectsBranchCutoffAndPages(t *testing.T) {
	t.Run("source-on-earlier-page", func(t *testing.T) {
		repo, db, claim, source, terminals := projectedToolOutcomeFixture(t)
		input := projectedToolOutcomeInput(t, repo, claim)
		input.AfterPublicationSequence = source.PublicationSeq - 1
		input.Limit = 1
		first, err := repo.ListProjectedEvents(context.Background(), input)
		if err != nil || len(first) != 1 || first[0].Event.EventID != source.EventID {
			t.Fatalf("source page=%+v err=%v", first, err)
		}
		input.AfterPublicationSequence = first[0].Event.PublicationSeq
		second, err := repo.ListProjectedEvents(context.Background(), input)
		if err != nil || len(second) != 1 || second[0].Event.EventID != terminals[0].EventID {
			t.Fatalf("terminal page=%+v err=%v", second, err)
		}
		assertProjectedToolOutcome(t, second, terminals[0].EventID, projectedToolOutcomeWant(source, terminals[0]))
		var payload []byte
		if err := db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`,
			claim.StreamUID, terminals[0].EventID).Scan(&payload); err != nil || string(payload) != string(second[0].Event.PayloadJSON) {
			t.Fatalf("page changed immutable terminal bytes: err=%v", err)
		}
	})

	t.Run("historical-branch-not-active-sibling", func(t *testing.T) {
		repo, db, claim, source, terminals := projectedToolOutcomeFixture(t)
		input := projectedToolOutcomeInput(t, repo, claim)
		seedProjectedToolOutcomeBranch(t, db, claim, input.BranchID, "br_0123abcd", 0)
		mustHistoricalReplayExec(t, db, `UPDATE transcript_branch_state SET active_branch_id=?,generation=generation+1 WHERE stream_uid=?`,
			"br_0123abcd", claim.StreamUID)
		// The historical branch uses the current validated generation; its
		// settled batch retains older read authority, not execution authority.
		input.BranchGeneration++
		history, err := repo.ListProjectedEvents(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		assertProjectedToolOutcome(t, history, terminals[0].EventID, projectedToolOutcomeWant(source, terminals[0]))
		input.BranchID = "br_0123abcd"
		sibling, err := repo.ListProjectedEvents(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		assertProjectedToolOutcomePageUnchanged(t, history, sibling)
		assertProjectedToolOutcome(t, sibling, terminals[0].EventID, nil)
		input.BranchID, input.BranchGeneration, input.ThroughPublicationSequence = "", 0, 0
		active, err := repo.ListProjectedEvents(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		assertProjectedToolOutcome(t, active, terminals[0].EventID, nil)
	})

	for _, test := range []struct {
		name, state string
		generation  int
		want        bool
	}{
		{"older-settled", "settled", 0, true},
		{"older-cancelled", "cancelled", 0, true},
		{"older-outcome-unknown", "outcome_unknown", 0, true},
		{"older-open", "running", 0, false},
		{"future-generation", "settled", 2, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, claim, source, terminals := projectedToolOutcomeFixture(t)
			mustHistoricalReplayExec(t, db, `UPDATE transcript_branch_state SET generation=2 WHERE stream_uid=?`, claim.StreamUID)
			mustHistoricalReplayExec(t, db, `UPDATE transcript_tool_call_batches SET state=?,branch_generation=? WHERE batch_id=?`,
				test.state, test.generation+1, "page-batch")
			projected, err := repo.ListProjectedEvents(context.Background(), projectedToolOutcomeInput(t, repo, claim))
			if err != nil {
				t.Fatal(err)
			}
			var want *RunnerReplayToolOutcome
			if test.want {
				want = projectedToolOutcomeWant(source, terminals[0])
			}
			assertProjectedToolOutcome(t, projected, terminals[0].EventID, want)
		})
	}

	t.Run("source-must-belong-to-requested-branch", func(t *testing.T) {
		repo, db, claim, source, terminals := projectedToolOutcomeFixture(t)
		input := projectedToolOutcomeInput(t, repo, claim)
		seedProjectedToolOutcomeBranch(t, db, claim, input.BranchID, "br_4567abcd", source.EventID)
		mustHistoricalReplayExec(t, db, `UPDATE transcript_tool_call_batches SET branch_id=? WHERE batch_id=?`, "br_4567abcd", "page-batch")
		input.BranchID = "br_4567abcd"
		projected, err := repo.ListProjectedEvents(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		assertProjectedToolOutcome(t, projected, terminals[0].EventID, nil)
	})

	t.Run("cutoff-excludes-future-source-from-ambiguity", func(t *testing.T) {
		repo, db, claim, source, terminals := projectedToolOutcomeFixture(t)
		input := projectedToolOutcomeInput(t, repo, claim)
		appendRunnerReplayToolBatchRoot(t, repo, claim, "future-source-batch", "settled", []runnerReplayTestCall{{
			ID: "call-0", Name: "read_file", Arguments: map[string]any{"path": "future-notes.txt"},
		}})
		setRunnerReplayToolBatchTerminals(t, db, "future-source-batch", terminals)
		later := appendRunnerReplayCheckpoint(t, repo, claim, "future-terminal", `{"status":"completed","toolPhase":"completed"}`)
		projected, err := repo.ListProjectedEvents(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		assertProjectedToolOutcome(t, projected, terminals[0].EventID, projectedToolOutcomeWant(source, terminals[0]))
		for _, event := range projected {
			if event.Event.PublicationSeq > input.ThroughPublicationSequence || event.Event.EventID == later.EventID {
				t.Fatal("provenance opt-in widened the fixed through-cutoff")
			}
		}
	})

	t.Run("legacy-and-default-do-not-require-batch-authority", func(t *testing.T) {
		repo, db, _ := newTranscriptRepository(t)
		claim := seedArtifactProjectionClaim(t, repo, db, "stream-page-legacy", "owner-a")
		input := projectedToolOutcomeInput(t, repo, claim)
		legacy, err := repo.ListProjectedEvents(context.Background(), input)
		if err != nil || len(legacy) == 0 {
			t.Fatalf("legacy page=%+v err=%v", legacy, err)
		}
		for _, event := range legacy {
			if event.ToolOutcome != nil {
				t.Fatal("legacy payload acquired batch provenance")
			}
		}
		mustHistoricalReplayExec(t, db, `CREATE TABLE transcript_tool_call_batches(batch_id TEXT PRIMARY KEY)`)
		input.IncludeToolOutcomes = false
		ordinary, err := repo.ListProjectedEvents(context.Background(), input)
		if err != nil {
			t.Fatalf("default page touched incomplete optional batch authority: %v", err)
		}
		assertProjectedToolOutcomePageUnchanged(t, legacy, ordinary)
		input.IncludeToolOutcomes = true
		if _, err := repo.ListProjectedEvents(context.Background(), input); err == nil {
			t.Fatal("opt-in silently accepted incomplete batch authority")
		}
	})

	t.Run("owner-and-generation-stay-fenced", func(t *testing.T) {
		repo, _, claim, _, _ := projectedToolOutcomeFixture(t)
		input := projectedToolOutcomeInput(t, repo, claim)
		input.OwnerID = "another-owner"
		if _, err := repo.ListProjectedEvents(context.Background(), input); !errors.Is(err, ErrOwnerMismatch) {
			t.Fatalf("foreign-owner error=%v", err)
		}
		input.OwnerID = claim.OwnerID
		input.BranchGeneration++
		if _, err := repo.ListProjectedEvents(context.Background(), input); !errors.Is(err, ErrBranchStateStale) {
			t.Fatalf("stale-generation error=%v", err)
		}
	})
}

func projectedToolOutcomeFixture(t *testing.T) (*Repository, *sql.DB, RunnerClaim, Event, []Event) {
	t.Helper()
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "stream-page-outcomes", "owner-a")
	installRunnerReplayToolBatchTables(t, db)
	source, terminals := seedRunnerReplayToolBatch(t, repo, db, claim, "page-batch", "settled", 1)
	return repo, db, claim, source, terminals
}

func projectedToolOutcomeInput(t *testing.T, repo *Repository, claim RunnerClaim) ListProjectedEventsInput {
	t.Helper()
	snapshot, err := repo.GetProjectionSnapshot(context.Background(), claim.StreamUID, claim.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	return ListProjectedEventsInput{StreamUID: claim.StreamUID, OwnerID: claim.OwnerID,
		BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
		ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 100, IncludeToolOutcomes: true}
}

func projectedToolOutcomeWant(source, terminal Event) *RunnerReplayToolOutcome {
	return &RunnerReplayToolOutcome{BatchID: "page-batch", SourceEventID: source.EventID,
		Ordinal: 0, ToolCallID: "call-0", TerminalEventID: terminal.EventID}
}

func assertProjectedToolOutcome(t *testing.T, events []ProjectedEvent, eventID int64, want *RunnerReplayToolOutcome) {
	t.Helper()
	got := projectedToolOutcomeForEvent(t, events, eventID)
	if (got == nil) != (want == nil) || (got != nil && *got != *want) {
		t.Fatalf("event %d canonical outcome=%+v want=%+v", eventID, got, want)
	}
}

func projectedToolOutcomeForEvent(t *testing.T, events []ProjectedEvent, eventID int64) *RunnerReplayToolOutcome {
	t.Helper()
	for _, event := range events {
		if event.Event.EventID == eventID {
			return event.ToolOutcome
		}
	}
	t.Fatalf("page omitted expected event %d", eventID)
	return nil
}

func assertProjectedToolOutcomePageUnchanged(t *testing.T, before, after []ProjectedEvent) {
	t.Helper()
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("private provenance changed page rows, order, payload, artifacts, or public JSON:\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
	}
}

func seedProjectedToolOutcomeBranch(t *testing.T, db *sql.DB, claim RunnerClaim, parent, branch string, omitEventID int64) {
	t.Helper()
	mustHistoricalReplayExec(t, db, `INSERT INTO transcript_branches(
		stream_uid,branch_id,parent_branch_id,fork_event_id,fork_point,kind,client_mutation_id,
		request_sha256,source_message_id,created_at,updated_at)
		SELECT ?,?,?,MIN(event_id),0,'edit',?,zeroblob(32),'source-user',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP
		FROM transcript_branch_events WHERE stream_uid=? AND branch_id=?`,
		claim.StreamUID, branch, parent, branch+"-mutation", claim.StreamUID, parent)
	mustHistoricalReplayExec(t, db, `INSERT INTO transcript_branch_events(stream_uid,branch_id,ordinal,event_id)
		SELECT stream_uid,?,ROW_NUMBER() OVER (ORDER BY ordinal),event_id FROM transcript_branch_events
		WHERE stream_uid=? AND branch_id=? AND event_id<>? ORDER BY ordinal`, branch, claim.StreamUID, parent, omitEventID)
}
