package workspace

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestNamespaceComputeProofPreservesMutableProgressButFencesInput(t *testing.T) {
	ctx := context.Background()
	store := namespaceV73Fixture(t)
	hardware := fmt.Sprintf(`{"archive_sha256":%q,"outputs":["out/**"],"logical_elapsed_seconds":0,"checkpoint_generation":0}`, strings.Repeat("a", 64))
	insert := `INSERT INTO compute_workbench_jobs(job_id,owner_user_id,project_id,environment,tier_type,provider,state,started_at,provider_family,provider_label,external_id,intent_json,hardware_json)
		VALUES(?,'owner','project','environment','remote','provider','running',CURRENT_TIMESTAMP,'ssh','SSH','actual-job','"description"',?)`
	if _, err := store.db.Exec(insert, "original-job", hardware); err != nil {
		t.Fatal(err)
	}
	originalResult := fmt.Sprintf(`{"output_manifest_sha256":%q,"checkpoint":{"receipt":{"manifest_sha256":%q}}}`, strings.Repeat("c", 64), strings.Repeat("d", 64))
	if _, err := store.db.Exec(`INSERT INTO compute_usage(id,job_id,environment,tier_type,provider,started_at,result) VALUES('usage','original-job','environment','remote','provider',CURRENT_TIMESTAMP,?)`, originalResult); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(strings.Replace(insert, "'actual-job'", "NULL", 1), "not-yet-launched", hardware); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE compute_workbench_jobs SET state='staging',external_id=NULL WHERE job_id='not-yet-launched'`); err != nil {
		t.Fatal(err)
	}
	namespaceApplyCutover(t, store)
	proof, found, err := store.GetComputeNamespaceCutoverReceipt(ctx, "owner", "original-job")
	if err != nil || !found || proof.InputSHA256 != strings.Repeat("a", 64) || proof.ExternalID != "actual-job" || proof.OriginalOutputManifestSHA256 != strings.Repeat("c", 64) || proof.OriginalCheckpointManifestSHA256 != strings.Repeat("d", 64) {
		t.Fatalf("proof=%#v found=%t err=%v", proof, found, err)
	}
	if _, found, err := store.GetComputeNamespaceCutoverReceipt(ctx, "foreign-owner", "original-job"); err != nil || found {
		t.Fatalf("foreign found=%t err=%v", found, err)
	}
	if _, err := store.db.Exec(`UPDATE compute_workbench_jobs SET state='completed',stdout_text='new log',hardware_json=json_set(hardware_json,'$.logical_elapsed_seconds',100,'$.checkpoint_generation',2) WHERE job_id='original-job'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE compute_usage SET result=json_set(result,'$.output_manifest_sha256',?) WHERE job_id='original-job'`, strings.Repeat("e", 64)); err != nil {
		t.Fatal(err)
	}
	updated, found, err := store.GetComputeNamespaceCutoverReceipt(ctx, "owner", "original-job")
	if err != nil || !found || updated.SourceSHA256 != proof.SourceSHA256 || updated.OriginalOutputManifestSHA256 != proof.OriginalOutputManifestSHA256 {
		t.Fatalf("normal progress invalidated proof: %#v found=%t err=%v", updated, found, err)
	}
	if _, err := store.db.Exec(`UPDATE compute_workbench_jobs SET state='running',external_id='new-canonical-runtime' WHERE job_id='not-yet-launched'`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.GetComputeNamespaceCutoverReceipt(ctx, "owner", "not-yet-launched"); err != nil || found {
		t.Fatalf("newly launched original staging row gained historical authority: found=%v err=%v", found, err)
	}
	if _, err := store.db.Exec(insert, "late-job", hardware); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.GetComputeNamespaceCutoverReceipt(ctx, "owner", "late-job"); err != nil || found {
		t.Fatalf("late found=%t err=%v", found, err)
	}
	if _, err := store.db.Exec(`UPDATE compute_workbench_jobs SET hardware_json=json_set(hardware_json,'$.archive_sha256',?) WHERE job_id='original-job'`, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetComputeNamespaceCutoverReceipt(ctx, "owner", "original-job"); err == nil {
		t.Fatal("changed submission input acquired historical proof")
	}
}
