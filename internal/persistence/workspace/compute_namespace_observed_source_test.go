package workspace

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func computeObservedSourceFixture(t *testing.T) (*Store, ComputeNamespaceObservedSourceReceipt) {
	t.Helper()
	store := namespaceV73Fixture(t)
	hardware := fmt.Sprintf(`{"archive_sha256":%q,"outputs":["out/**"]}`, strings.Repeat("a", 64))
	if _, err := store.db.Exec(`INSERT INTO compute_workbench_jobs(job_id,owner_user_id,project_id,environment,tier_type,provider,state,started_at,provider_family,provider_label,external_id,intent_json,hardware_json) VALUES('old-job','owner','project','environment','remote','provider','running',CURRENT_TIMESTAMP,'ssh','SSH','actual-runtime','"description"',?)`, hardware); err != nil {
		t.Fatal(err)
	}
	namespaceApplyCutover(t, store)
	frozen, found, err := store.GetComputeNamespaceCutoverReceipt(context.Background(), "owner", "old-job")
	if err != nil || !found {
		t.Fatalf("frozen found=%v err=%v", found, err)
	}
	return store, ComputeNamespaceObservedSourceReceipt{JobID: frozen.JobID, OwnerUserID: frozen.OwnerUserID, SourceKind: "checkpoint", SourceSHA256: frozen.SourceSHA256, InputSHA256: frozen.InputSHA256, Environment: frozen.Environment, Provider: frozen.Provider, ExternalID: frozen.ExternalID, ManifestSHA256: strings.Repeat("b", 64)}
}

func TestNamespaceObservedCheckpointIsImmutableOwnerAndRuntimeBound(t *testing.T) {
	ctx := context.Background()
	store, input := computeObservedSourceFixture(t)
	if _, found, err := store.GetComputeNamespaceObservedSource(ctx, "owner", input.JobID, "checkpoint", input.ManifestSHA256); err != nil || found {
		t.Fatalf("unobserved found=%v err=%v", found, err)
	}
	first, err := store.RecordComputeNamespaceObservedSource(ctx, "owner", input)
	if err != nil || first.CreatedAtMS <= 0 {
		t.Fatalf("recorded=%#v err=%v", first, err)
	}
	again, err := store.RecordComputeNamespaceObservedSource(ctx, "owner", input)
	if err != nil || again != first {
		t.Fatalf("idempotence=%#v err=%v", again, err)
	}
	if _, found, err := store.GetComputeNamespaceObservedSource(ctx, "foreign", input.JobID, "checkpoint", input.ManifestSHA256); err != nil || found {
		t.Fatalf("foreign found=%v err=%v", found, err)
	}
	for name, mutate := range map[string]func(*ComputeNamespaceObservedSourceReceipt){
		"owner":       func(v *ComputeNamespaceObservedSourceReceipt) { v.OwnerUserID = "foreign" },
		"input":       func(v *ComputeNamespaceObservedSourceReceipt) { v.InputSHA256 = strings.Repeat("c", 64) },
		"snapshot":    func(v *ComputeNamespaceObservedSourceReceipt) { v.SourceSHA256 = strings.Repeat("c", 64) },
		"runtime":     func(v *ComputeNamespaceObservedSourceReceipt) { v.ExternalID = "other-runtime" },
		"environment": func(v *ComputeNamespaceObservedSourceReceipt) { v.Environment = "other" },
		"provider":    func(v *ComputeNamespaceObservedSourceReceipt) { v.Provider = "other" },
		"kind":        func(v *ComputeNamespaceObservedSourceReceipt) { v.SourceKind = "unproven-output" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := input
			mutate(&changed)
			if _, err := store.RecordComputeNamespaceObservedSource(ctx, "owner", changed); err == nil {
				t.Fatal("changed observed source admitted")
			}
		})
	}
	if _, err := store.db.Exec(`UPDATE compute_namespace_observed_source_receipts SET manifest_sha256=?`, strings.Repeat("c", 64)); err == nil {
		t.Fatal("observed receipt rewritten")
	}
	if _, err := store.db.Exec(`DELETE FROM compute_namespace_observed_source_receipts`); err == nil {
		t.Fatal("observed receipt deleted while job exists")
	}
	// Mutable result metadata cannot manufacture an offline source receipt.
	if _, err := store.db.Exec(`INSERT INTO compute_usage(id,job_id,environment,tier_type,provider,started_at,result) VALUES('usage','old-job','environment','remote','provider',CURRENT_TIMESTAMP,?)`, fmt.Sprintf(`{"checkpoint":{"receipt":{"manifest_sha256":%q}}}`, strings.Repeat("c", 64))); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.GetComputeNamespaceObservedSource(ctx, "owner", input.JobID, "checkpoint", strings.Repeat("c", 64)); err != nil || found {
		t.Fatalf("mutable result laundered receipt: found=%v err=%v", found, err)
	}
	loaded, found, err := store.GetComputeNamespaceObservedSource(ctx, "owner", input.JobID, "checkpoint", input.ManifestSHA256)
	if err != nil || !found || loaded != first {
		t.Fatalf("original=%#v found=%v err=%v", loaded, found, err)
	}
	if _, err := store.db.Exec(`INSERT INTO compute_workbench_jobs(job_id,owner_user_id,project_id,environment,tier_type,provider,state,started_at,provider_family,provider_label,external_id,intent_json,hardware_json) SELECT 'new-job',owner_user_id,project_id,environment,tier_type,provider,state,started_at,provider_family,provider_label,'new-runtime',intent_json,hardware_json FROM compute_workbench_jobs WHERE job_id='old-job'`); err != nil {
		t.Fatal(err)
	}
	newInput := input
	newInput.JobID = "new-job"
	newInput.ExternalID = "new-runtime"
	if _, err := store.RecordComputeNamespaceObservedSource(ctx, "owner", newInput); err == nil {
		t.Fatal("post-cutover current job acquired historical source authority")
	}
	if _, err := store.db.Exec(`UPDATE compute_workbench_jobs SET external_id='replacement-runtime' WHERE job_id='old-job'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetComputeNamespaceObservedSource(ctx, "owner", input.JobID, "checkpoint", input.ManifestSHA256); err == nil {
		t.Fatal("source receipt replayed across runtimes")
	}
}

func TestNamespaceObservedCheckpointWriteFailurePreservesPhysicalJob(t *testing.T) {
	ctx := context.Background()
	store, input := computeObservedSourceFixture(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_observed_source BEFORE INSERT ON compute_namespace_observed_source_receipts BEGIN SELECT RAISE(ABORT,'injected source receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordComputeNamespaceObservedSource(ctx, "owner", input); err == nil {
		t.Fatal("injected receipt write succeeded")
	}
	var count int
	var state string
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM compute_namespace_observed_source_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("receipt count=%d err=%v", count, err)
	}
	if err := store.db.QueryRow(`SELECT state FROM compute_workbench_jobs WHERE job_id='old-job'`).Scan(&state); err != nil || state != "running" {
		t.Fatalf("physical state=%s err=%v", state, err)
	}
}
