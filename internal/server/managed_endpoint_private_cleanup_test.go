//go:build linux

package server

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/persistence/workspace"
)

func TestManagedEndpointPrivateCleanupRunsOnlyAfterDeleteCommit(t *testing.T) {
	fixture := newManagedEndpointPrivateCleanupFixture(t)
	if deleted, err := fixture.store.DeleteManagedEndpoint(fixture.instance.Endpoint, fixture.instance.UserID); err != nil || !deleted {
		t.Fatalf("delete did not commit: deleted=%v err=%v", deleted, err)
	}
	if _, err := os.Stat(fixture.directory); err != nil {
		t.Fatalf("delete performed filesystem cleanup before the delivery worker: %v", err)
	}
	events := managedEndpointPrivateCleanupEvents(t, fixture.store)
	if len(events) != 1 {
		t.Errorf("delete lost its per-instance durable private cleanup intent: %d", len(events))
	}
	for _, event := range events {
		for _, field := range []string{"startScript", "stopScript", "credentialName"} {
			if strings.Contains(string(event.Payload), `"`+field+`"`) {
				t.Errorf("private cleanup copied execution/credential authority %q into payload", field)
			}
		}
	}
	if deleted, err := fixture.store.DeleteManagedEndpoint(fixture.instance.Endpoint, fixture.instance.UserID); err != nil || deleted {
		t.Fatalf("repeated delete was not a no-op: deleted=%v err=%v", deleted, err)
	}
	if count := managedEndpointPrivateCleanupCount(t, fixture.store, `SELECT COUNT(*) FROM workspace_outbox WHERE topic=? AND event_type=?`, workspace.RealtimeOutboxTopic, "managed_endpoint_removed"); count != 1 {
		t.Errorf("public removed event was not retained exactly once: %d", count)
	}
	runManagedEndpointPrivateCleanupRound(t, fixture.app)
	if _, err := os.Lstat(fixture.directory); !os.IsNotExist(err) {
		t.Errorf("retired owner/action/output files survived committed cleanup: %v", err)
	}
	for _, path := range []string{fixture.cache, fixture.unknown} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("cleanup removed cache or unrelated owner %q: %v", path, err)
		}
	}
	for _, event := range managedEndpointPrivateCleanupEvents(t, fixture.store) {
		if event.Status != workspace.OutboxStatusDelivered {
			t.Errorf("committed private cleanup was not acknowledged: %+v", event)
		}
	}
}

func TestManagedEndpointPrivateCleanupRejectsUntrustedOwnerAndCanReplay(t *testing.T) {
	for _, boundary := range []string{"descriptor_mismatch", "instance_directory_symlink"} {
		t.Run(boundary, func(t *testing.T) {
			fixture := newManagedEndpointPrivateCleanupFixture(t)
			var restore func()
			var protected string
			if boundary == "descriptor_mismatch" {
				foreign := fixture.instance
				foreign.UserID = "different-owner"
				writeManagedEndpointPrivateCleanupJSON(t, filepath.Join(fixture.directory, "owner.json"), foreign)
				protected = filepath.Join(fixture.directory, "output-0")
				restore = func() { writeManagedEndpointPrivateCleanupJSON(t, filepath.Join(fixture.directory, "owner.json"), fixture.instance) }
			} else {
				saved := fixture.directory + ".saved"
				if err := os.Rename(fixture.directory, saved); err != nil {
					t.Fatal(err)
				}
				outside := t.TempDir()
				writeManagedEndpointPrivateCleanupJSON(t, filepath.Join(outside, "owner.json"), fixture.instance)
				protected = filepath.Join(outside, "output-0")
				writeManagedEndpointPrivateCleanupFile(t, protected, []byte("outside owner must survive"))
				if err := os.Symlink(outside, fixture.directory); err != nil {
					t.Fatal(err)
				}
				restore = func() {
					if err := os.Remove(fixture.directory); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(saved, fixture.directory); err != nil {
						t.Fatal(err)
					}
				}
			}
			if deleted, err := fixture.store.DeleteManagedEndpoint(fixture.instance.Endpoint, fixture.instance.UserID); err != nil || !deleted {
				t.Fatalf("SQL delete unexpectedly depended on filesystem delivery: %v %v", deleted, err)
			}
			events := managedEndpointPrivateCleanupEvents(t, fixture.store)
			if len(events) != 1 {
				t.Fatalf("deleted owner has no recoverable cleanup intent: %d", len(events))
			}
			event := events[0]
			// Make generic dead-letter/replay observable in one attempt without
			// sleeping through production backoff or fabricating an owner/lease.
			if err := fixture.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.ExecContext(t.Context(), `UPDATE workspace_outbox SET max_attempts=1 WHERE event_id=? AND status='pending'`, event.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			runManagedEndpointPrivateCleanupRound(t, fixture.app)
			if _, err := os.Stat(protected); err != nil {
				t.Errorf("untrusted cleanup touched a foreign/unknown private file: %v", err)
			}
			failed, err := fixture.store.GetOutboxEvent(t.Context(), event.ID)
			if err != nil || failed.Status != workspace.OutboxStatusDeadLetter || failed.LastError == "" || failed.AttemptCount != 1 {
				t.Fatalf("cleanup failure was hidden or not replayable: %+v %v", failed, err)
			}
			restore()
			if err := fixture.store.RequeueOutboxDeadLetter(t.Context(), event.ID); err != nil {
				t.Fatal(err)
			}
			runManagedEndpointPrivateCleanupRound(t, fixture.app)
			if _, err := os.Lstat(fixture.directory); !os.IsNotExist(err) {
				t.Errorf("same durable cleanup did not resume after repair: %v", err)
			}
			replayed, err := fixture.store.GetOutboxEvent(t.Context(), event.ID)
			if err != nil || replayed.Status != workspace.OutboxStatusDelivered {
				t.Errorf("replayed cleanup did not acknowledge the original intent: %+v %v", replayed, err)
			}
			for _, path := range []string{fixture.cache, fixture.unknown} {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("replay removed an unrelated path %q: %v", path, err)
				}
			}
		})
	}
}

func TestManagedEndpointPrivateCleanupEnqueueFailureRollsBackDelete(t *testing.T) {
	fixture := newManagedEndpointPrivateCleanupFixture(t)
	if err := fixture.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER deny_private_cleanup BEFORE INSERT ON workspace_outbox
			WHEN NEW.topic='compute.managed-endpoint-private-cleanup'
			BEGIN SELECT RAISE(ABORT,'controlled private cleanup enqueue failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := fixture.store.DeleteManagedEndpoint(fixture.instance.Endpoint, fixture.instance.UserID); err == nil || deleted {
		t.Errorf("delete committed without its durable cleanup intent: deleted=%v err=%v", deleted, err)
	}
	endpoints, err := fixture.store.ListManagedEndpoints(fixture.instance.UserID, fixture.instance.Endpoint, false)
	if err != nil || len(endpoints) != 1 || endpoints[0].State != "stopped" {
		t.Errorf("enqueue failure lost the stopped registration: %+v %v", endpoints, err)
	}
	if count := managedEndpointPrivateCleanupCount(t, fixture.store, `SELECT COUNT(*) FROM compute_managed_endpoint_instances WHERE instance_id=? AND retired_at IS NOT NULL`, fixture.instance.ID); count != 1 {
		t.Errorf("enqueue failure lost retired owner recovery evidence: %d", count)
	}
	if count := managedEndpointPrivateCleanupCount(t, fixture.store, `SELECT COUNT(*) FROM workspace_outbox WHERE topic=? AND event_type=?`, workspace.RealtimeOutboxTopic, "managed_endpoint_removed"); count != 0 {
		t.Errorf("rolled-back delete leaked a public removed event: %d", count)
	}
	if events := managedEndpointPrivateCleanupEvents(t, fixture.store); len(events) != 0 {
		t.Errorf("failed transaction leaked cleanup intents: %d", len(events))
	}
	if _, err := os.Stat(filepath.Join(fixture.directory, "owner.json")); err != nil {
		t.Errorf("rolled-back delete removed private owner evidence: %v", err)
	}
}
