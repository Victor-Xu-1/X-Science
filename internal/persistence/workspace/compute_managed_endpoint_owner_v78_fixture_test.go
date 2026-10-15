package workspace

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"x-science/internal/compute"
	transcriptstore "x-science/internal/persistence/transcript"
)

type managedEndpointV77UpgradeFixture struct {
	db       *sql.DB
	path     string
	journal  []SchemaMigrationRecord
	retained map[string][]string
}

// Build an installed schema77 through the real predecessor registry. In
// particular, do not install the proposed owner DDL or call its test fixture.
func newManagedEndpointV77UpgradeFixture(t *testing.T) managedEndpointV77UpgradeFixture {
	t.Helper()
	db, path := openTargetTwentyTwoProviderDB(t)
	applyTranscriptMigration(t, db, 77)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for index, state := range []string{"live", "stopping", "failed"} {
		registration := compute.ManagedEndpointRegistration{
			Name: "legacy-" + state, URL: "http://127.0.0.1:25100", Port: 25100,
			SkillName: "using-model-endpoint", CredentialName: "fixture-credential-ref", LivePath: "/health",
			StartScript: "printf 'original start\\n'", StopScript: "printf 'original stop\\n'",
		}
		token := ""
		if state == "stopping" {
			token = "original-private-claim-token"
		}
		if _, err := db.Exec(`INSERT INTO compute_managed_endpoints
			(name,owner_user_id,url,port,state,location,skill_name,credential_name,live_path,
			start_script,stop_script,approved_script_hash,last_error,transcript,state_changed_at,
			service_dir,service_dir_bytes,stop_claim_token,stop_claim_generation)
			VALUES(?,?,?, ?,?,'local',?,?,?, ?,?,?,?, ?,?,?,?, ?,?)`,
			registration.Name, "migration-owner", registration.URL, registration.Port, state,
			registration.SkillName, registration.CredentialName, registration.LivePath,
			registration.StartScript, registration.StopScript, compute.ApprovedManagedEndpointHash(registration),
			"retained diagnostic", "original output\n保留\n", now, t.TempDir(), 321+index, token, 7+index); err != nil {
			t.Fatal(err)
		}
	}
	seedManagedEndpointV77Input(t, db)
	status, err := (&Store{db: db}).SchemaStatus(t.Context())
	if err != nil || status.CurrentVersion != 77 || len(status.Migrations) != 77 {
		t.Fatalf("invalid installed schema77 fixture: %+v %v", status, err)
	}
	return managedEndpointV77UpgradeFixture{db: db, path: path, journal: status.Migrations, retained: managedEndpointV77RetainedRows(t, db)}
}

func seedManagedEndpointV77Input(t *testing.T, db *sql.DB) {
	t.Helper()
	repo := transcriptstore.NewRepository(db)
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "owner-migration-input", OwnerID: "migration-owner", ExternalID: "owner-migration-input",
		Kind: transcriptstore.StreamKindStandalone, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := repo.AppendUserEvent(t.Context(), transcriptstore.AppendUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "retained-input-1",
		PayloadJSON: []byte(`{"role":"user","text":"Original input 保留"}`),
	}); err != nil || !created {
		t.Fatalf("schema77 first input: created=%t %v", created, err)
	}
	claim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "retained-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("schema77 runner claim: %+v %v", claim, err)
	}
	if _, _, created, err := repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim.Claim, ClientMessageID: "retained-checkpoint", Phase: transcriptstore.RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: []byte(`{"completed_steps":2,"summary":"retain exact recovery bytes"}`),
	}); err != nil || !created {
		t.Fatalf("schema77 checkpoint: created=%t %v", created, err)
	}
	if _, err := repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{
		Claim: claim.Claim, ClientMessageID: "retained-interruption", ReasonCode: "runtime_draining", AutoResume: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, created, err := repo.AppendUserEvent(t.Context(), transcriptstore.AppendUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "retained-input-2",
		PayloadJSON: []byte(`{"role":"user","text":"Later input must remain admitted"}`),
	}); err != nil || !created {
		t.Fatalf("schema77 later input: created=%t %v", created, err)
	}
	var facts, revision, consumed int
	if err := db.QueryRow(`SELECT input_revision,consumed_input_revision,
		(SELECT COUNT(*) FROM transcript_input_admissions WHERE stream_uid=?)
		FROM transcript_streams WHERE stream_uid=?`, stream.UID, stream.UID).Scan(&revision, &consumed, &facts); err != nil || facts != 2 || revision != 2 || consumed != 0 {
		t.Fatalf("invalid schema77 admission fixture: R=%d C=%d facts=%d %v", revision, consumed, facts, err)
	}
}

// Select only predecessor endpoint columns: adding a nullable owner column is
// expected, while every original cell (including NULL/blob/text/time) is not.
func managedEndpointV77RetainedRows(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	queries := map[string]string{
		"endpoints": `SELECT name,owner_user_id,url,port,state,location,skill_name,credential_name,live_path,
			start_script,stop_script,approved_script_hash,last_error,transcript,state_changed_at,
			service_dir,service_dir_bytes,stop_claim_token,stop_claim_generation FROM compute_managed_endpoints ORDER BY name`,
		"streams":     `SELECT * FROM transcript_streams ORDER BY stream_uid`,
		"events":      `SELECT * FROM transcript_events ORDER BY stream_uid,event_id`,
		"admissions":  `SELECT * FROM transcript_input_admissions ORDER BY stream_uid,input_revision`,
		"branches":    `SELECT * FROM transcript_branch_state ORDER BY stream_uid`,
		"membership":  `SELECT * FROM transcript_branch_events ORDER BY stream_uid,branch_id,ordinal`,
		"attempts":    `SELECT * FROM transcript_runner_attempts ORDER BY stream_uid,attempt`,
		"checkpoints": `SELECT * FROM transcript_runner_checkpoints ORDER BY stream_uid,checkpoint_sequence`,
		"receipts":    `SELECT * FROM transcript_runner_receipts ORDER BY stream_uid,attempt`,
	}
	retained := make(map[string][]string, len(queries))
	for label, query := range queries {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for i := range values {
				destinations[i] = &values[i]
			}
			if err := rows.Scan(destinations...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			retained[label] = append(retained[label], string(encoded))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		_ = rows.Close()
	}
	return retained
}

func (fixture managedEndpointV77UpgradeFixture) assertRetained(t *testing.T, db *sql.DB) {
	t.Helper()
	if got := managedEndpointV77RetainedRows(t, db); !reflect.DeepEqual(fixture.retained, got) {
		t.Fatal("owner migration changed predecessor endpoint/input/branch/recovery cells")
	}
	status, err := (&Store{db: db}).SchemaStatus(t.Context())
	if err != nil || len(status.Migrations) < 77 || !reflect.DeepEqual(fixture.journal, status.Migrations[:77]) {
		t.Fatalf("owner migration changed the first77 journal identities/timestamps: %+v %v", status, err)
	}
}
