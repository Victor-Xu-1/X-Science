package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	adaptercommon "x-science/internal/adapters/common"
	secretstore "x-science/internal/persistence/secrets"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func TestIMHistoricalNoticeCannotBorrowCompletedInputAcrossNewerControl(t *testing.T) {
	for _, retainedOrdinary := range []bool{false, true} {
		name := "only_completed_ordinary_before_control"
		if retainedOrdinary {
			name = "retained_ordinary_then_newer_control"
		}
		t.Run(name, func(t *testing.T) {
			store, repo, db, stream := historicalIMControlSourceFixture(t, retainedOrdinary)
			evidence, err := repo.GetFreshMessageEvidence(t.Context(), stream.UID, stream.OwnerID)
			if err != nil || evidence.Authorized || evidence.Coverage.Complete || !evidence.Coverage.Pending() {
				t.Fatalf("control fixture is not actual incomplete/unauthorized history: %+v err=%v", evidence, err)
			}
			var calls atomic.Int32
			app, dispatcher, settled := historicalNoticeServer(t, t.TempDir(), store, repo, stream, nil,
				func(context.Context, adaptercommon.IMSessionBinding, adaptercommon.ServerMessage) (adaptercommon.OutboundReport, error) {
					calls.Add(1)
					return adaptercommon.OutboundReport{}, nil
				})
			if _, err := app.RecoverIMOutbound(t.Context()); err != nil {
				t.Fatal(err)
			}
			var notices int
			if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE event_type='host_recovery_notice'`).Scan(&notices); err != nil {
				t.Fatal(err)
			}
			if notices > 0 {
				awaitHistoricalNotice(t, settled) // Observe the real wrong send, not only a row.
			}
			closeTranscriptIMTestDispatcher(t, dispatcher)
			if notices != 0 || calls.Load() != 0 {
				t.Fatalf("newer typed control borrowed an older ordinary source: notices=%d sends=%d err=%v", notices, calls.Load(), err)
			}
		})
	}
}

// A complete private76 predecessor and its old source/counter writer are
// reconstructed; this is not a claim that current IM exposes a Frame approval
// API. Imported standalone history may carry valid typed control events, while
// today's normal IM Stage+Admit always creates user_message. Neither case may
// borrow an older ordinary input for this limited user-visible notice.
func historicalIMControlSourceFixture(t *testing.T, retainedOrdinary bool) (*workspace.Store, *transcriptstore.Repository, *sql.DB, transcriptstore.Stream) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control-history.db")
	store, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	var facts int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM transcript_streams)+(SELECT COUNT(*) FROM transcript_input_admissions)+
		(SELECT COUNT(*) FROM compute_managed_endpoint_instances)+(SELECT COUNT(*) FROM compute_managed_endpoint_actions)+
		(SELECT COUNT(*) FROM compute_managed_endpoints WHERE owner_spec_json IS NOT NULL)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("not an empty prospective fixture: facts=%d err=%v", facts, err)
	}
	restoreHistoricalIMV76EmptyProjectionSchema(t, db)
	for _, statement := range []string{`DROP TABLE compute_managed_endpoint_actions`, `DROP TABLE compute_managed_endpoint_instances`,
		`ALTER TABLE compute_managed_endpoints DROP COLUMN owner_spec_json`, `DROP TABLE transcript_input_admissions`,
		`DELETE FROM workspace_schema_migrations WHERE version>=77`} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var versions, version int
	if err := db.QueryRow(`SELECT COUNT(*),MAX(version) FROM workspace_schema_migrations`).Scan(&versions, &version); err != nil || versions != 76 || version != 76 {
		t.Fatalf("not the full76 predecessor: count=%d max=%d err=%v", versions, version, err)
	}
	repo := transcriptstore.NewRepository(db)
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: "control-im", OwnerID: secretstore.DefaultUserID,
		SessionID: "im:wechat:historical", ExternalID: "im:wechat:historical", Kind: transcriptstore.StreamKindStandalone, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	stage := func(id string, payload []byte) transcriptstore.Event {
		t.Helper()
		staged, err := repo.StageUserEvent(t.Context(), transcriptstore.AppendUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
			ClientMessageID: id, PayloadJSON: payload})
		if err != nil || !staged.Created || staged.Admitted {
			t.Fatalf("historical stage=%+v err=%v", staged, err)
		}
		return staged.Event
	}
	admit := func(event transcriptstore.Event, kind string) {
		t.Helper()
		if err := repo.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
			result, err := tx.ExecContext(t.Context(), `UPDATE transcript_events SET event_type=? WHERE stream_uid=? AND event_id=?
				AND event_type='user_message_staged' AND runner_attempt IS NULL`, kind, stream.UID, event.EventID)
			if err != nil {
				return err
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				return transcriptstore.ErrEventConflict
			}
			_, err = tx.ExecContext(t.Context(), `UPDATE transcript_streams SET input_revision=input_revision+1,updated_at=? WHERE stream_uid=?`, event.CreatedAt, stream.UID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	admit(stage("completed-input", []byte(`{"text":"Already completed ordinary instruction"}`)), "user_message")
	var retained transcriptstore.Event
	if retainedOrdinary {
		retained = stage("retained-ordinary", []byte(`{"text":"Retained ordinary input"}`))
	}
	claim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		RunnerID: "original-owner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claim.Claimed || claim.Claim.ClaimedInputRevision != 1 {
		t.Fatalf("original claim=%+v err=%v", claim, err)
	}
	if _, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: claim.Claim, ClientMessageID: "finished-original",
		Status: "completed", PayloadJSON: []byte(`{"text":"Original result"}`)}); err != nil {
		t.Fatal(err)
	}
	if retainedOrdinary {
		admit(retained, "user_message")
	}
	controlPayload := []byte(`{"planApprovalId":"approved-plan","role":"user","messageOrigin":"input_response","content":[{"type":"tool_result","tool_use_id":"question","content":"approved"}]}`)
	control := stage("typed-control", controlPayload)
	admit(control, "user_input_response")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err = store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	stream, err = repo.GetStream(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	var kind, preserved string
	if err := db.QueryRow(`SELECT event_type,payload_json FROM transcript_events WHERE stream_uid=? ORDER BY event_id DESC LIMIT 1`, stream.UID).Scan(&kind, &preserved); err != nil || kind != "user_input_response" || preserved != string(controlPayload) {
		t.Fatalf("control source changed: kind=%s payload=%s err=%v", kind, preserved, err)
	}
	wantRevision := int64(2)
	if retainedOrdinary {
		wantRevision++
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions`).Scan(&facts); err != nil || facts != 0 || stream.ConsumedInputRevision != 1 || stream.InputRevision != wantRevision {
		t.Fatalf("historical coverage fabricated: facts=%d stream=%+v err=%v", facts, stream, err)
	}
	return store, repo, db, stream
}
