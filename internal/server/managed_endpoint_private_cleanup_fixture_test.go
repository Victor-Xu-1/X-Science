//go:build linux

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

// This is an internal delivery topic, not a public realtime event or a second
// endpoint scheduler. A literal keeps this regression compilable before its fix.
const managedEndpointPrivateCleanupTestTopic = "compute.managed-endpoint-private-cleanup"

type managedEndpointPrivateCleanupRuntime struct {
	*managedEndpointAPIRuntime
	instance compute.ManagedEndpointOwnerInstance
	action   compute.ManagedEndpointAction
}

func (runtime *managedEndpointPrivateCleanupRuntime) Stop(ctx context.Context, instance compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction, values map[string]string) (compute.ManagedEndpointOwnerReceipt, error) {
	receipt, err := runtime.managedEndpointAPIRuntime.Stop(ctx, instance, action, values)
	if err == nil {
		runtime.instance = instance
		runtime.action = compute.ManagedEndpointAction{Ref: action.Ref, Accepted: true, Receipt: &receipt}
	}
	return receipt, err
}

type managedEndpointPrivateCleanupFixture struct {
	app       *Server
	store     *workspace.Store
	instance  compute.ManagedEndpointOwnerInstance
	directory string
	cache     string
	unknown   string
}

func newManagedEndpointPrivateCleanupFixture(t *testing.T) managedEndpointPrivateCleanupFixture {
	t.Helper()
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runtime := &managedEndpointPrivateCleanupRuntime{managedEndpointAPIRuntime: &managedEndpointAPIRuntime{store: store}}
	app := New(Options{Workspace: store, FileRoot: t.TempDir(), ManagedEndpointRuntime: runtime})
	registration := managedEndpointAPIFixture(t, store, "private-cleanup-owner")
	if err := app.ensureManagedInferenceEndpointReady(t.Context(), workspace.KernelFrameAccess{UserID: "owner-a"}, registration.Name); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.registerComputeWorkbenchRoutes(mux)
	requestJSON(t, mux, http.MethodPost, "/api/compute/managed-endpoints/"+registration.Name+"/stop", nil, "owner-a", http.StatusOK)
	if runtime.starts != 1 || runtime.stops != 1 || runtime.action.Receipt == nil || !runtime.action.Receipt.HasPositiveStoppedProof(runtime.instance) {
		t.Fatalf("fixture did not finish the normal owner lifecycle: %+v", runtime.action)
	}
	instance := runtime.instance
	if err := instance.Validate(); err != nil {
		t.Fatal(err)
	}
	fixture := managedEndpointPrivateCleanupFixture{app: app, store: store, instance: instance,
		directory: filepath.Join(instance.RuntimeRoot, instance.ID),
		cache:     filepath.Join(instance.ServiceDir, "cache", "model-weights"),
		unknown:   filepath.Join(instance.RuntimeRoot, "unrelated-owner", "output-0")}
	writeManagedEndpointPrivateCleanupJSON(t, filepath.Join(fixture.directory, "owner.json"), instance)
	writeManagedEndpointPrivateCleanupJSON(t, filepath.Join(fixture.directory, "action.json"), runtime.action)
	for path, text := range map[string]string{filepath.Join(fixture.directory, "output-0"): "private endpoint output", fixture.cache: "preserved model cache", fixture.unknown: "unknown private owner"} {
		writeManagedEndpointPrivateCleanupFile(t, path, []byte(text))
	}
	return fixture
}

func writeManagedEndpointPrivateCleanupJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeManagedEndpointPrivateCleanupFile(t, path, raw)
}

func writeManagedEndpointPrivateCleanupFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func managedEndpointPrivateCleanupEvents(t *testing.T, store *workspace.Store) []workspace.OutboxEvent {
	t.Helper()
	var ids []string
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(t.Context(), `SELECT event_id FROM workspace_outbox WHERE topic=? ORDER BY sequence`, managedEndpointPrivateCleanupTestTopic)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	events := make([]workspace.OutboxEvent, 0, len(ids))
	for _, id := range ids {
		event, err := store.GetOutboxEvent(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func runManagedEndpointPrivateCleanupRound(t *testing.T, app *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err := app.RunManagedEndpointOwners(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func managedEndpointPrivateCleanupCount(t *testing.T, store *workspace.Store, query string, values ...any) int {
	t.Helper()
	var count int
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), query, values...).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
