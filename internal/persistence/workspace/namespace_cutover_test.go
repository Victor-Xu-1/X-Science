package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	transcriptstore "x-science/internal/persistence/transcript"
)

func namespaceV73Fixture(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	store := &Store{db: db, now: time.Now, blobRoot: path + ".blobs"}
	if err := prepareSchemaJournal(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := store.prepareLegacyWorkspaceSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(context.Background(), db, time.Now, 73); err != nil {
		t.Fatal(err)
	}
	store.transcriptRepository = transcriptstore.NewRepository(db)
	return store
}

func namespaceApplyCutover(t *testing.T, store *Store) {
	t.Helper()
	if err := applyVersionedSchemaMigrationsThrough(context.Background(), store.db, store.now, 74); err != nil {
		t.Fatal(err)
	}
}

func TestNamespacePublishedSpecificationRejectsChangedCanonicalContent(t *testing.T) {
	for index, migration := range workspaceSchemaMigrations[:73] {
		checksum, err := migration.validatedChecksum()
		if err != nil || checksum != publishedMigrationIdentities[index].published {
			t.Fatalf("migration %d=%s err=%v", index+1, checksum, err)
		}
		changed := append([]versionedSchemaMigration(nil), workspaceSchemaMigrations...)
		changed[index].name += "-changed"
		if err := validateSchemaMigrationIdentityRegistry(changed); err == nil || !strings.Contains(err.Error(), "canonical specification identity mismatch") {
			t.Fatalf("modified migration %d admitted: %v", index+1, err)
		}
	}
}

func TestNamespaceCutoverRetainsPublishedJournalAndCompletedOutboxReceipt(t *testing.T) {
	ctx := context.Background()
	store := namespaceV73Fixture(t)
	input := EnqueueOutboxInput{Topic: "test.delivery", IdempotencyKey: "same-request", PartitionKey: "owner", Type: "completed", Payload: json.RawMessage(`{"value":1}`)}
	input.ID = historicalOutboxEventID(input.Topic, input.IdempotencyKey)
	before, err := store.EnqueueOutbox(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE workspace_outbox SET status='delivered',delivered_at_ms=123 WHERE event_id=?`, before.ID); err != nil {
		t.Fatal(err)
	}
	journal, err := store.SchemaStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	namespaceApplyCutover(t, store)
	input.ID = ""
	replayed, err := store.EnqueueOutbox(ctx, input)
	if err != nil || replayed.ID != before.ID || replayed.Status != OutboxStatusDelivered || replayed.Sequence != before.Sequence {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
	if _, err := store.EnqueueOutbox(ctx, input); err != nil {
		t.Fatal(err)
	}
	after, err := store.SchemaStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index, row := range journal.Migrations {
		if row != after.Migrations[index] {
			t.Fatalf("published journal %d changed", row.Version)
		}
	}
	var events, aliases int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM workspace_outbox`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM namespace_identity_aliases`).Scan(&aliases); err != nil {
		t.Fatal(err)
	}
	if events != 1 || aliases != 1 {
		t.Fatalf("events=%d aliases=%d", events, aliases)
	}
	input.Payload = json.RawMessage(`{"value":2}`)
	if _, err := store.EnqueueOutbox(ctx, input); err == nil {
		t.Fatal("changed receipt content was accepted")
	}
}

func TestNamespaceCutoverRejectsLateHistoricalIdentityAndAliasCollision(t *testing.T) {
	ctx := context.Background()
	store := namespaceV73Fixture(t)
	input := EnqueueOutboxInput{ID: "historic-effect", Topic: "test.delivery", IdempotencyKey: "existing", Type: "test"}
	if _, err := store.EnqueueOutbox(ctx, input); err != nil {
		t.Fatal(err)
	}
	namespaceApplyCutover(t, store)
	if err := store.WithTransaction(ctx, func(tx *sql.Tx) error {
		resolved, err := store.ResolveNamespaceIdentityTx(ctx, tx, "outbox", "canonical-effect", input.ID)
		if err != nil {
			return err
		}
		if resolved != input.ID {
			t.Fatalf("resolved=%s", resolved)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTransaction(ctx, func(tx *sql.Tx) error {
		_, err := store.ResolveNamespaceIdentityTx(ctx, tx, "outbox", "competing-effect", input.ID)
		return err
	}); err == nil {
		t.Fatal("one historical effect gained two canonical identities")
	}
	late := EnqueueOutboxInput{ID: "late-effect", Topic: "test.delivery", IdempotencyKey: "late", Type: "test"}
	if _, err := store.EnqueueOutbox(ctx, late); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTransaction(ctx, func(tx *sql.Tx) error {
		identity, err := store.ResolveNamespaceIdentityTx(ctx, tx, "outbox", "new-late-effect", late.ID)
		if err != nil {
			return err
		}
		if identity != "new-late-effect" {
			t.Fatal("post-cutover effect acquired a legacy alias")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO namespace_cutover_receipts VALUES('outbox','late-effect')`); err == nil {
		t.Fatal("sealed cutoff inventory admitted a late identity")
	}
	if _, err := store.db.Exec(`UPDATE namespace_identity_aliases SET existing_id='late-effect'`); err == nil {
		t.Fatal("alias was rewritten")
	}
	late.ID = ""
	if _, err := store.EnqueueOutbox(ctx, late); err == nil {
		t.Fatal("late unbound identity was duplicated")
	}
}

func TestNamespaceCutoverMigrationFailureIsAtomic(t *testing.T) {
	store := namespaceV73Fixture(t)
	failed := namespaceCutoverV74Migration
	failed.statements = append(append([]string(nil), failed.statements...), "INSERT INTO missing_namespace_table VALUES(1)")
	checksum, err := failed.validatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigration(context.Background(), store.db, store.now, failed, checksum); err == nil {
		t.Fatal("injected cutover failure succeeded")
	}
	var tables, version int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('namespace_cutover_receipts','namespace_identity_aliases')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT MAX(version) FROM workspace_schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || version != 73 {
		t.Fatalf("partial migration tables=%d version=%d", tables, version)
	}
	namespaceApplyCutover(t, store)
}

func TestNamespaceCutoverPreservesCompletedRealtimeEffect(t *testing.T) {
	ctx := WithMutationIdempotencyKey(context.Background(), "request-1")
	store := namespaceV73Fixture(t)
	if _, err := store.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Retained"}); err != nil {
		t.Fatal(err)
	}
	legacy := historicalDomainRealtimeEventID("request-1", "artifact_created", "artifact")
	envelope := RealtimeOutboxEnvelope{Version: 1, Event: RealtimeEventInput{ID: legacy, UserID: "owner", ProjectID: "project", Type: "artifact_created", Payload: map[string]any{"project_id": "project", "artifact_id": "artifact"}}}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	input := EnqueueOutboxInput{ID: historicalOutboxEventID(RealtimeOutboxTopic, legacy), IdempotencyKey: legacy, Topic: RealtimeOutboxTopic, PartitionKey: "user:owner", Type: "artifact_created", AggregateType: "project", AggregateID: "project", Payload: raw, MaxAttempts: realtimeOutboxMaxSend}
	if _, err := store.EnqueueOutbox(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE workspace_outbox SET status='delivered',delivered_at_ms=123 WHERE event_id=?`, input.ID); err != nil {
		t.Fatal(err)
	}
	namespaceApplyCutover(t, store)
	for index := 0; index < 2; index++ {
		if err := store.WithTransaction(ctx, func(tx *sql.Tx) error {
			return store.enqueueProjectDomainEventTx(ctx, tx, "owner", "project", "artifact_created", "artifact", map[string]any{"artifact_id": "artifact"})
		}); err != nil {
			t.Fatal(err)
		}
	}
	var count, delivered int
	if err := store.db.QueryRow(`SELECT COUNT(*),SUM(status='delivered') FROM workspace_outbox`).Scan(&count, &delivered); err != nil {
		t.Fatal(err)
	}
	if count != 1 || delivered != 1 {
		t.Fatalf("effects=%d delivered=%d", count, delivered)
	}
}

func TestNamespaceNativeEventProofIsOwnerBytesAndCutoffBound(t *testing.T) {
	ctx := context.Background()
	store := namespaceV73Fixture(t)
	_, repository, claim := initializeKernelLocalOperationFixture(t, store)
	payload := []byte(`{"interruption_cause":{"schema":"synon.runner_interruption_cause.v1","reason_code":"transient_provider_error","detail":"retained"}}`)
	_, event, _, err := repository.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{Claim: claim, ClientMessageID: "historical-native", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload})
	if err != nil {
		t.Fatal(err)
	}
	namespaceApplyCutover(t, store)
	if valid, err := repository.IsHistoricalNamespaceEvent(ctx, "owner", event); err != nil || !valid {
		t.Fatalf("native proof=%t err=%v", valid, err)
	}
	if valid, err := repository.IsHistoricalNamespaceEvent(ctx, "foreign-owner", event); err != nil || valid {
		t.Fatalf("foreign proof=%t err=%v", valid, err)
	}
	changed := event
	changed.PayloadJSON = []byte(`{"changed":true}`)
	if valid, err := repository.IsHistoricalNamespaceEvent(ctx, "owner", changed); err != nil || valid {
		t.Fatalf("changed bytes proof=%t err=%v", valid, err)
	}
	_, late, _, err := repository.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{Claim: claim, ClientMessageID: "late-native", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload})
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := repository.IsHistoricalNamespaceEvent(ctx, "owner", late); err != nil || valid {
		t.Fatalf("late proof=%t err=%v", valid, err)
	}
	var original []byte
	if err := store.db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, event.StreamUID, event.EventID).Scan(&original); err != nil || string(original) != string(payload) {
		t.Fatalf("original source changed err=%v", err)
	}
}
