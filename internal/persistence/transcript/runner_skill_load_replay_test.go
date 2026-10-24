package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRunnerSkillLoadCheckpointsPreserveOriginalOrderAndArguments(t *testing.T) {
	repo, db, dsn := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "skill-load-originals", "owner-a")
	raw := `{"status":"completed","toolPhase":"completed","toolName":"Skill","toolInput":{"skill":"wrong-request"},"executedToolInput":{"skill":"\u2003/ÉTUDE\u2003","args":"{  \"Z\": 2,\n  \"a\": \"原始\" }","filter":"^Part [12]$"},"toolResult":{"prompt":"original rendered instructions"},"skillLoadReceipt":{"invocationKey":"original-key"}}`
	original := appendRunnerReplayCheckpoint(t, repo, claim, "original", raw)
	appendRunnerReplayCheckpoint(t, repo, claim, "other-root", `{"status":"completed","toolPhase":"completed","toolName":"skill","toolInput":{"skill":"other"},"toolResult":"other instructions"}`)
	failed := appendRunnerReplayCheckpoint(t, repo, claim, "failed-result", `{"status":"completed","toolPhase":"completed","toolName":"skill","executedToolInput":{"skill":"étude"},"toolResult":{"success":false,"error":"preserve the actual failure"}}`)
	unknown := appendRunnerReplayCheckpoint(t, repo, claim, "unknown-execution", `{"status":"completed","toolPhase":"completed","toolName":"skill","toolInput":{"skill":"another-request"},"executedToolInput":null,"toolResult":"completed but unassignable"}`)
	appendRunnerReplayCheckpoint(t, repo, claim, "not-terminal", `{"status":"running","toolPhase":"started","toolName":"skill","toolInput":{"skill":"étude"}}`)
	appendRunnerReplayCheckpoint(t, repo, claim, "compact", `{"status":"completed","toolPhase":"auto_compact","summary":"not execution authority"}`)
	input := runnerSkillLoadReplayInput(t, repo, claim)
	input.SkillName = " /étude "
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	repo = NewRepository(reopened)
	got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input)
	if err != nil || len(got) != 3 {
		t.Fatalf("original checkpoints=%+v err=%v", got, err)
	}
	for index, want := range []Event{unknown, failed, original} {
		if got[index].Event.EventID != want.EventID || string(got[index].ResolvedPayloadJSON) != string(want.PayloadJSON) {
			t.Fatalf("checkpoint %d changed identity, order or bytes: got=%+v want=%+v", index, got[index], want)
		}
	}
	if string(got[2].Event.PayloadJSON) != raw || string(got[2].ResolvedPayloadJSON) != raw {
		t.Fatal("original raw arguments or result bytes were rewritten")
	}
	input.Limit = 1
	latest, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input)
	if err != nil || len(latest) != 1 || latest[0].Event.EventID != unknown.EventID {
		t.Fatalf("unknown latest execution revived an older success: got=%+v err=%v", latest, err)
	}
}

func TestRunnerSkillLoadCheckpointsKeyAndFullResultShape(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "skill-load-shapes", "owner-a")
	appendResult := func(id, key string, result any) Event {
		payload, err := json.Marshal(map[string]any{
			"status": "completed", "toolPhase": "completed", "toolName": "skill",
			"executedToolInput": map[string]any{"skill": "cohort"}, "toolResult": result,
			"skillLoadReceipt": map[string]any{"invocationKey": key},
		})
		if err != nil {
			t.Fatal(err)
		}
		return appendRunnerReplayCheckpoint(t, repo, claim, id, string(payload))
	}
	inline := appendResult("inline", "host-key", "original inline instructions")
	prompt := appendResult("prompt", "host-key", map[string]any{"prompt": "rendered instructions"})
	descriptor := appendResult("externalized", "host-key", map[string]any{
		"artifact_id": "artifact-original", "version_id": "version-original", "preview": "not full instructions",
	})
	appendResult("other-key", "another-key", "different host invocation")
	appendResult("preview-only", "host-key", map[string]any{"preview": "not instructions"})
	appendResult("wrong-descriptor-type", "host-key", map[string]any{"artifact_id": 1, "version_id": "version-original"})
	for index := 0; index < 40; index++ {
		appendResult(fmt.Sprintf("compact-%d", index), "host-key", map[string]any{"status": "already_loaded"})
	}
	input := runnerSkillLoadReplayInput(t, repo, claim)
	input.InvocationKey, input.FullResultOnly = "host-key", true
	got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input)
	if err != nil || len(got) != 3 {
		t.Fatalf("full result candidates=%+v err=%v", got, err)
	}
	for index, want := range []Event{descriptor, prompt, inline} {
		if got[index].Event.EventID != want.EventID {
			t.Fatalf("full candidates[%d]=%d want=%d", index, got[index].Event.EventID, want.EventID)
		}
	}
	input.InvocationKey = "host-key' OR 1=1 --"
	if got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input); err != nil || len(got) != 0 {
		t.Fatalf("invocation key was not bound exactly: got=%+v err=%v", got, err)
	}
	input.InvocationKey, input.SkillName = "", "cohort' OR 1=1 --"
	if got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input); err != nil || len(got) != 0 {
		t.Fatalf("root was not compared as data: got=%+v err=%v", got, err)
	}
}

func TestRunnerSkillLoadCheckpointsScopeAttemptsAndInput(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "skill-load-attempts", "owner-a")
	first := appendRunnerReplayCheckpoint(t, repo, claim, "first-input-skill", `{"status":"completed","toolPhase":"completed","toolName":"skill","toolInput":{"skill":"cohort"},"toolResult":"original"}`)
	frozen := runnerSkillLoadReplayInput(t, repo, claim)
	if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{
		Claim: claim, ClientMessageID: "finish-first", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	// This read-side fixture records a later same-input runtime row directly.
	// It does not exercise, bypass, or claim to prove execution admission.
	mustHistoricalReplayExec(t, db, `INSERT INTO transcript_runner_attempts(
		stream_uid,attempt,runner_id,claim_token_sha256,claimed_input_revision,resume_source,status,phase,claimed_at,expires_at)
		SELECT stream_uid,attempt+1,'fixture-successor',claim_token_sha256,claimed_input_revision,'retry','running','executing',claimed_at,expires_at
		FROM transcript_runner_attempts WHERE stream_uid=? AND attempt=?`, claim.StreamUID, claim.Attempt)
	successor := claim
	successor.Attempt++
	input := runnerSkillLoadReplayInput(t, repo, successor)
	got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input)
	if err != nil || len(got) != 1 || got[0].Event.EventID != first.EventID {
		t.Fatalf("earlier same-input checkpoint=%+v err=%v", got, err)
	}
	if _, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), frozen); !errors.Is(err, ErrClaimStale) {
		t.Fatalf("obsolete current-attempt coordinate err=%v", err)
	}
	if _, _, err := repo.AppendUserEvent(t.Context(), AppendUserEventInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, ClientMessageID: "new-input", PayloadJSON: []byte(`{"text":"a genuinely new task"}`),
	}); err != nil {
		t.Fatal(err)
	}
	mustHistoricalReplayExec(t, db, `UPDATE transcript_runner_attempts SET claimed_input_revision=claimed_input_revision+1
		WHERE stream_uid=? AND attempt=?`, claim.StreamUID, successor.Attempt)
	successor.ClaimedInputRevision++
	input = runnerSkillLoadReplayInput(t, repo, successor)
	if got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input); err != nil || len(got) != 0 {
		t.Fatalf("older input leaked into current attempt: got=%+v err=%v", got, err)
	}
	input.InputRevision = claim.ClaimedInputRevision
	if _, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input); !errors.Is(err, ErrClaimStale) {
		t.Fatalf("caller-selected older input err=%v", err)
	}
}

func TestRunnerSkillLoadCheckpointsFrozenProjectionAndExactEvent(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "skill-load-frozen", "owner-a")
	// Deliberately separate event identity from publication order so a point
	// reader cannot accidentally compare the event ID against the cutoff.
	mustHistoricalReplayExec(t, db, `UPDATE transcript_streams SET next_event_id=next_event_id+1000 WHERE stream_uid=?`, claim.StreamUID)
	first := appendRunnerReplayCheckpoint(t, repo, claim, "first", `{"status":"completed","toolPhase":"completed","toolName":"skill","toolInput":{"skill":"cohort"},"toolResult":"first"}`)
	input := runnerSkillLoadReplayInput(t, repo, claim)
	later := appendRunnerReplayCheckpoint(t, repo, claim, "later", `{"status":"completed","toolPhase":"completed","toolName":"skill","toolInput":{"skill":"cohort"},"toolResult":"later"}`)
	got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input)
	if err != nil || len(got) != 1 || got[0].Event.EventID != first.EventID {
		t.Fatalf("frozen cutoff moved: got=%+v err=%v", got, err)
	}
	projection := ListProjectedEventsInput{
		StreamUID: input.StreamUID, OwnerID: input.OwnerID, BranchID: input.BranchID,
		BranchGeneration: input.BranchGeneration, ThroughPublicationSequence: input.ThroughPublicationSequence,
	}
	point, found, err := repo.GetProjectedEventByID(t.Context(), projection, first.EventID)
	if err != nil || !found || point.Event.EventID != first.EventID || string(point.ResolvedPayloadJSON) != string(first.PayloadJSON) {
		t.Fatalf("exact event=%+v found=%t err=%v", point, found, err)
	}
	for _, absent := range []int64{later.EventID, 1 << 60} {
		if _, found, err := repo.GetProjectedEventByID(t.Context(), projection, absent); err != nil || found {
			t.Fatalf("out-of-projection id %d found=%t err=%v", absent, found, err)
		}
	}
	projection.AfterPublicationSequence = first.PublicationSeq
	if _, found, err := repo.GetProjectedEventByID(t.Context(), projection, first.EventID); err != nil || found {
		t.Fatalf("exact event ignored lower cursor: found=%t err=%v", found, err)
	}
	projection.AfterPublicationSequence = 0
	for _, test := range []struct {
		name   string
		change func(*ListRunnerSkillLoadCheckpointsInput, *ListProjectedEventsInput)
		want   error
	}{
		{"owner", func(a *ListRunnerSkillLoadCheckpointsInput, b *ListProjectedEventsInput) {
			a.OwnerID, b.OwnerID = "other-owner", "other-owner"
		}, ErrOwnerMismatch},
		{"generation", func(a *ListRunnerSkillLoadCheckpointsInput, b *ListProjectedEventsInput) {
			a.BranchGeneration++
			b.BranchGeneration++
		}, ErrBranchStateStale},
		{"cutoff", func(a *ListRunnerSkillLoadCheckpointsInput, b *ListProjectedEventsInput) {
			a.ThroughPublicationSequence += 1000
			b.ThroughPublicationSequence += 1000
		}, ErrBranchStateStale},
		{"missing-cutoff", func(a *ListRunnerSkillLoadCheckpointsInput, b *ListProjectedEventsInput) {
			a.ThroughPublicationSequence, b.ThroughPublicationSequence = 0, 0
		}, ErrEventConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, b := input, projection
			test.change(&a, &b)
			if _, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), a); !errors.Is(err, test.want) {
				t.Fatalf("checkpoint scope err=%v want=%v", err, test.want)
			}
			if _, _, err := repo.GetProjectedEventByID(t.Context(), b, first.EventID); !errors.Is(err, test.want) {
				t.Fatalf("point scope err=%v want=%v", err, test.want)
			}
		})
	}
	seedProjectedToolOutcomeBranch(t, db, claim, input.BranchID, "br_0123abcd", first.EventID)
	mustHistoricalReplayExec(t, db, `UPDATE transcript_branch_state SET active_branch_id=?,generation=generation+1 WHERE stream_uid=?`, "br_0123abcd", claim.StreamUID)
	if _, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input); !errors.Is(err, ErrBranchStateStale) {
		t.Fatalf("inactive frozen branch err=%v", err)
	}
	if _, _, err := repo.GetProjectedEventByID(t.Context(), projection, first.EventID); !errors.Is(err, ErrBranchStateStale) {
		t.Fatalf("inactive point branch err=%v", err)
	}
	input.BranchID, projection.BranchID = "br_0123abcd", "br_0123abcd"
	input.BranchGeneration++
	projection.BranchGeneration++
	if got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input); err != nil || len(got) != 0 {
		t.Fatalf("missing branch membership borrowed old checkpoint: got=%+v err=%v", got, err)
	}
	if _, found, err := repo.GetProjectedEventByID(t.Context(), projection, first.EventID); err != nil || found {
		t.Fatalf("missing branch membership borrowed exact event: found=%t err=%v", found, err)
	}
}

func TestRunnerSkillLoadCheckpointsUseCanonicalFramePayloadAndOutcome(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "skill-load-frame", "owner-a")
	installRunnerReplayToolBatchTables(t, db)
	source := appendRunnerReplayToolBatchRoot(t, repo, claim, "skill-load-batch", "settled", []runnerReplayTestCall{{
		ID: "skill-call", Name: "skill", Arguments: map[string]any{"skill": "cohort"},
	}})
	payload := `{"status":"completed","toolPhase":"completed","toolName":"skill","toolCallId":"skill-call","executedToolInput":{"skill":"cohort","args":"raw  args"},"toolResult":{"prompt":"canonical frame instructions"}}`
	terminal := appendRunnerReplayCheckpoint(t, repo, claim, "frame-terminal", payload)
	setRunnerReplayToolBatchTerminals(t, db, "skill-load-batch", []Event{terminal})
	frameID := "skill-frame-terminal"
	mustHistoricalReplayExec(t, db, `INSERT INTO frame_events(id,frame_id,sequence,event_type,payload,created_at) VALUES(?, 'frame-a', 100, 'runner_checkpoint', ?, CURRENT_TIMESTAMP)`, frameID, payload)
	mustHistoricalReplayExec(t, db, `UPDATE transcript_events SET source='frame_ref',payload_json=NULL,frame_event_id=? WHERE stream_uid=? AND event_id=?`, frameID, claim.StreamUID, terminal.EventID)
	input := runnerSkillLoadReplayInput(t, repo, claim)
	got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input)
	if err != nil || len(got) != 1 || got[0].Event.PayloadJSON != nil || string(got[0].ResolvedPayloadJSON) != payload {
		t.Fatalf("canonical frame checkpoint=%+v err=%v", got, err)
	}
	want := RunnerReplayToolOutcome{BatchID: "skill-load-batch", SourceEventID: source.EventID, Ordinal: 0, ToolCallID: "skill-call", TerminalEventID: terminal.EventID}
	if got[0].ToolOutcome == nil || *got[0].ToolOutcome != want {
		t.Fatalf("canonical relation=%+v want=%+v", got[0].ToolOutcome, want)
	}
	projection := ListProjectedEventsInput{StreamUID: input.StreamUID, OwnerID: input.OwnerID, BranchID: input.BranchID,
		BranchGeneration: input.BranchGeneration, ThroughPublicationSequence: input.ThroughPublicationSequence}
	point, found, err := repo.GetProjectedEventByID(t.Context(), projection, source.EventID)
	if err != nil || !found || string(point.ResolvedPayloadJSON) != string(source.PayloadJSON) {
		t.Fatalf("original source event=%+v found=%t err=%v", point, found, err)
	}
	mustHistoricalReplayExec(t, db, `UPDATE frame_events SET frame_id='foreign-frame' WHERE id=?`, frameID)
	if _, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("foreign canonical frame err=%v", err)
	}
	if _, _, err := repo.GetProjectedEventByID(t.Context(), projection, terminal.EventID); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("foreign canonical exact event err=%v", err)
	}
}

func TestRunnerSkillLoadCheckpointsBoundScanAndRejectInvalidCoordinates(t *testing.T) {
	for _, test := range []struct {
		name  string
		count int
		body  string
	}{
		{"events", DefaultRunnerReplayExpandedEvents + 1, "small instructions"},
		{"bytes", 34, strings.Repeat("x", MaxEventPayloadBytes-1024)},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, _ := newTranscriptRepository(t)
			claim := seedArtifactProjectionClaim(t, repo, db, "skill-load-budget", "owner-a")
			payload, err := json.Marshal(map[string]any{"status": "completed", "toolPhase": "completed", "toolName": "skill",
				"toolInput": map[string]any{"skill": "unrelated"}, "toolResult": test.body})
			if err != nil {
				t.Fatal(err)
			}
			seed := appendRunnerReplayCheckpoint(t, repo, claim, "budget-seed", string(payload))
			input := runnerSkillLoadReplayInput(t, repo, claim)
			seedRunnerSkillLoadScanRows(t, db, claim, input.BranchID, seed, test.count-1)
			input = runnerSkillLoadReplayInput(t, repo, claim)
			got, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), input)
			if !errors.Is(err, ErrSkillLoadReplayBudget) || errors.Is(err, ErrSchemaUnavailable) || got != nil {
				t.Fatalf("bounded scan returned false EOF/partial/schema failure: got=%+v err=%v", got, err)
			}
		})
	}
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "skill-load-validation", "owner-a")
	input := runnerSkillLoadReplayInput(t, repo, claim)
	for _, change := range []func(*ListRunnerSkillLoadCheckpointsInput){
		func(in *ListRunnerSkillLoadCheckpointsInput) { in.Limit = 0 },
		func(in *ListRunnerSkillLoadCheckpointsInput) { in.Limit = 33 },
		func(in *ListRunnerSkillLoadCheckpointsInput) { in.InputRevision = 0 },
		func(in *ListRunnerSkillLoadCheckpointsInput) { in.ThroughAttempt = 0 },
		func(in *ListRunnerSkillLoadCheckpointsInput) { in.BranchGeneration = 0 },
		func(in *ListRunnerSkillLoadCheckpointsInput) { in.BranchID = "" },
		func(in *ListRunnerSkillLoadCheckpointsInput) { in.SkillName = " / " },
	} {
		changed := input
		change(&changed)
		if _, err := repo.ListRunnerSkillLoadCheckpoints(t.Context(), changed); !errors.Is(err, ErrEventConflict) {
			t.Fatalf("invalid coordinates %+v err=%v", changed, err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repo.ListRunnerSkillLoadCheckpoints(canceled, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation err=%v", err)
	}
}

func runnerSkillLoadReplayInput(t *testing.T, repo *Repository, claim RunnerClaim) ListRunnerSkillLoadCheckpointsInput {
	t.Helper()
	snapshot, err := repo.GetProjectionSnapshot(t.Context(), claim.StreamUID, claim.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	return ListRunnerSkillLoadCheckpointsInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, BranchID: snapshot.BranchID,
		BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence,
		InputRevision: claim.ClaimedInputRevision, ThroughAttempt: claim.Attempt, SkillName: "cohort", Limit: 32,
	}
}

// Populate only an isolated SQLite fixture in one transaction; this is not a
// worker, model, real task, or a product repair that edits canonical history.
func seedRunnerSkillLoadScanRows(t *testing.T, db *sql.DB, claim RunnerClaim, branch string, seed Event, count int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`WITH RECURSIVE copies(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM copies WHERE n<?)
		INSERT INTO transcript_events(stream_uid,event_id,publication_seq,client_message_id,event_type,source,runner_attempt,payload_json,created_at)
		SELECT ?,?+n,?+n,'budget-copy-'||n,'runner_checkpoint','payload',?,?,CURRENT_TIMESTAMP FROM copies`,
		count, claim.StreamUID, seed.EventID, seed.PublicationSeq, claim.Attempt, seed.PayloadJSON); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO transcript_branch_events(stream_uid,branch_id,ordinal,event_id)
		SELECT ?,?,(SELECT MAX(ordinal) FROM transcript_branch_events WHERE stream_uid=? AND branch_id=?)+
			ROW_NUMBER() OVER (ORDER BY event_id),event_id
		FROM transcript_events WHERE stream_uid=? AND event_id>? ORDER BY event_id`,
		claim.StreamUID, branch, claim.StreamUID, branch, claim.StreamUID, seed.EventID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
