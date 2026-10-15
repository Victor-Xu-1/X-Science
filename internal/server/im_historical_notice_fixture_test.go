package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	secretstore "x-science/internal/persistence/secrets"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

// Only an empty private DB is reconstructed. Stage plus the original76 writer
// produces the old source/counter ordering; normal Open then installs77/78.
// No admission mapping, claim, lease, receipt or source payload is fabricated.
func historicalIMNoticeFixture(t *testing.T, state string) (*workspace.Store, *transcriptstore.Repository, *sql.DB, transcriptstore.Stream) {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "historical.db")
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
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM transcript_streams)+
		(SELECT COUNT(*) FROM transcript_input_admissions)+(SELECT COUNT(*) FROM compute_managed_endpoint_actions)+
		(SELECT COUNT(*) FROM compute_managed_endpoint_instances)+
		(SELECT COUNT(*) FROM compute_managed_endpoints WHERE owner_spec_json IS NOT NULL)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("historical predecessor must be empty: facts=%d err=%v", facts, err)
	}
	for _, statement := range []string{
		`DROP TABLE compute_managed_endpoint_actions`, `DROP TABLE compute_managed_endpoint_instances`,
		`ALTER TABLE compute_managed_endpoints DROP COLUMN owner_spec_json`,
		`DROP TABLE transcript_input_admissions`, `DELETE FROM workspace_schema_migrations WHERE version>=77`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var count, version int
	if err := db.QueryRow(`SELECT COUNT(*),MAX(version) FROM workspace_schema_migrations`).Scan(&count, &version); err != nil || count != 76 || version != 76 {
		t.Fatalf("not the complete76 predecessor: count=%d version=%d err=%v", count, version, err)
	}
	repo := transcriptstore.NewRepository(db)
	stream, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "historical-im", OwnerID: secretstore.DefaultUserID, SessionID: "im:wechat:historical",
		ExternalID: "im:wechat:historical", Kind: transcriptstore.StreamKindStandalone, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	stage := func(id string) transcriptstore.Event {
		t.Helper()
		result, err := repo.StageUserEvent(ctx, transcriptstore.AppendUserEventInput{StreamUID: stream.UID,
			OwnerID: stream.OwnerID, ClientMessageID: id, PayloadJSON: []byte(`{"text":"Retained task ` + id + `"}`)})
		if err != nil || !result.Created || result.Admitted {
			t.Fatalf("historical Stage=%+v err=%v", result, err)
		}
		return result.Event
	}
	admit76 := func(event transcriptstore.Event) {
		t.Helper()
		err := repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
			result, err := tx.ExecContext(ctx, `UPDATE transcript_events SET event_type='user_message'
				WHERE stream_uid=? AND event_id=? AND event_type='user_message_staged' AND runner_attempt IS NULL`, stream.UID, event.EventID)
			if err != nil {
				return err
			}
			if rows, err := result.RowsAffected(); err != nil || rows != 1 {
				return transcriptstore.ErrEventConflict
			}
			_, err = tx.ExecContext(ctx, `UPDATE transcript_streams SET input_revision=input_revision+1,updated_at=?
				WHERE stream_uid=? AND owner_id=?`, event.CreatedAt, stream.UID, stream.OwnerID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	admit76(stage("initial"))
	queued := stage("queued") // Source predates the actual claim/finish marker.
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		RunnerID: "historical-owner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatalf("historical claim=%+v err=%v", claimed, err)
	}
	if state == "manual" {
		_, err = repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{Claim: claimed.Claim, ClientMessageID: "manual-wait",
			ReasonCode: "user_owned_wait", Resumable: true, AutoResume: false, ResumeDetail: "Wait for the user"})
	} else if state != "live" {
		_, _, _, err = repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{Claim: claimed.Claim, ClientMessageID: "historical-finish",
			Status: state, PayloadJSON: []byte(`{"text":"Retained result"}`)})
	}
	if err != nil {
		t.Fatal(err)
	}
	admit76(queued)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err = store.TranscriptRepository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	stream, err = repo.GetStream(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions`).Scan(&facts); err != nil || facts != 0 || stream.InputRevision != 2 {
		t.Fatalf("upgrade invented admission: facts=%d U=%d err=%v", facts, stream.InputRevision, err)
	}
	var retained string
	if err := db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, stream.UID, queued.EventID).Scan(&retained); err != nil || retained != string(queued.PayloadJSON) {
		t.Fatalf("upgrade changed source: %q err=%v", retained, err)
	}
	evidence, err := repo.GetFreshMessageEvidence(context.Background(), stream.UID, stream.OwnerID)
	if err != nil || evidence.Authorized || evidence.Coverage.Complete || !evidence.Coverage.Pending() {
		t.Fatalf("fixture is not unknown historical input: %+v err=%v", evidence, err)
	}
	return store, repo, db, stream
}
