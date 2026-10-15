package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute/checkpoint"
	"x-science/internal/compute/namespace"
	"x-science/internal/compute/transfer"
	workspace "x-science/internal/persistence/workspace"
)

// Only this private temporary fixture is reconstructed before the actual cutover
// capture. No source hashes or cutover rows are fabricated by the test.
func captureServerNamespaceFixture(t *testing.T, store *workspace.Store, db *sql.DB) *workspace.Store {
	t.Helper()
	var seq int
	var name, path string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, table := range []string{"transcript_input_admissions", "compute_namespace_observed_source_receipts", "namespace_identity_aliases", "namespace_cutover_receipts", "kernel_runtime_identity_receipts", "namespace_kernel_operation_receipts", "namespace_transcript_event_receipts", "namespace_compute_job_receipts", "namespace_history_audit_receipts"} {
		if _, err := tx.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatal(err)
		}
	}
	// Reconstruct the actual v74 predecessor, including the unchanged v69
	// startup guard. Removing only the v75 journal row leaves a hole once v76
	// exists; retaining its newer trigger would also forge a historical shape.
	// The v77 prospective relation and its table-owned triggers must likewise
	// be absent in this private predecessor; source events/counters stay intact.
	if _, err := tx.Exec(`DROP TRIGGER kernel_startup_terminal_receipt`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`CREATE TRIGGER kernel_startup_terminal_receipt BEFORE UPDATE ON kernel_execution_backends
		WHEN OLD.state='starting' AND NEW.state='stopped' AND NOT EXISTS(
		SELECT 1 FROM kernel_execution_startup_receipts r WHERE r.backend_id=NEW.backend_id
		AND r.backend_generation=NEW.backend_generation AND r.executor_instance_id=NEW.executor_instance_id)
		BEGIN SELECT RAISE(ABORT,'kernel startup terminal receipt is required'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM workspace_schema_migrations WHERE version>=75`); err != nil {
		t.Fatal(err)
	}
	var count, version int
	if err := tx.QueryRow(`SELECT COUNT(*),MAX(version) FROM workspace_schema_migrations`).Scan(&count, &version); err != nil || count != 74 || version != 74 {
		t.Fatalf("historical fixture is not the complete v74 prefix: count=%d version=%d err=%v", count, version, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	return upgraded
}

func seedServerNamespaceComputeJob(t *testing.T, store *workspace.Store, hardware map[string]any, id string) (workspace.KernelFrameAccess, workspace.OwnedComputeJob) {
	t.Helper()
	seedTranscriptWebFrame(t, store, "namespace-owner", "namespace-project", "namespace-frame")
	if _, err := store.UpsertSSHProvider(workspace.ComputeProviderInput{Name: "ssh:namespace-fixture", UserID: "namespace-owner", Family: "ssh"}); err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccess("namespace-frame")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	frame, root := access.Frame.ID, access.Frame.RootFrameID
	hardware["root_frame_incarnation_id"] = access.RootFrameIncarnationID
	job, err := store.CreateComputeJob(access.UserID, workspace.ComputeJob{JobID: id, ProjectID: access.Frame.ProjectID,
		Provider: "ssh:namespace-fixture", ProviderFamily: "ssh", Environment: "remote", TierType: "remote",
		FrameID: &frame, RootFrameID: &root, HardwareDetails: hardware, Intent: "Observe the same original compute job"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.BindComputeJobExternal(access.UserID, id, "actual-original-runtime", "")
	if err != nil {
		t.Fatal(err)
	}
	return access, workspace.OwnedComputeJob{OwnerUserID: access.UserID, Job: job}
}

func TestNamespaceComputeContextUsesActualFrozenOwnerAndImmutableInput(t *testing.T) {
	ctx := context.Background()
	store, _, db := newTranscriptWebFixture(t)
	_, owned := seedServerNamespaceComputeJob(t, store, map[string]any{"archive_sha256": strings.Repeat("a", 64), "outputs": []any{"out/**"}}, "original-compute")
	store = captureServerNamespaceFixture(t, store, db)
	s := &Server{workspaceStore: store}
	observed, err := s.computeNamespaceContext(ctx, owned)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := computeNamespaceSourceProof(observed, strings.Repeat("b", 64))
	if err != nil || proof == nil || proof.VerifyRead(strings.Repeat("a", 64), strings.Repeat("b", 64)) != nil {
		t.Fatalf("proof=%v err=%v", proof, err)
	}
	foreign := owned
	foreign.OwnerUserID = "another-owner"
	if _, err := s.computeNamespaceContext(ctx, foreign); !errors.Is(err, namespace.ErrRecoveryUnconfirmed) {
		t.Fatal("foreign owner acquired historical evidence", err)
	}
	if _, err := db.Exec(`UPDATE compute_workbench_jobs SET hardware_json=json_set(hardware_json,'$.archive_sha256',?) WHERE job_id=?`, strings.Repeat("c", 64), owned.Job.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.computeNamespaceContext(ctx, owned); !errors.Is(err, namespace.ErrRecoveryUnconfirmed) {
		t.Fatal("changed original input admitted", err)
	}
}

func TestNamespaceComputeRecoveryHoldPreservesResultsAndDoesNotCompleteTask(t *testing.T) {
	ctx := context.Background()
	store, _, db := newTranscriptWebFixture(t)
	access, owned := seedServerNamespaceComputeJob(t, store, map[string]any{"archive_sha256": strings.Repeat("a", 64)}, "recovery-compute")
	prior := map[string]any{"output_manifest": "hpc/recovery-compute/original.jsonl", "stdout_tail": "original progress", "checkpoint": map[string]any{"original_receipt": "preserved"}}
	if err := store.SetComputeJobResult(access.UserID, owned.Job.JobID, prior); err != nil {
		t.Fatal(err)
	}
	s := &Server{workspaceStore: store}
	if !s.holdComputeNamespaceRecovery(owned, namespace.ErrRecoveryUnconfirmed) {
		t.Fatal("bounded recovery observation was not committed")
	}
	job, found, err := store.GetComputeJob(access.UserID, owned.Job.JobID)
	if err != nil || !found || job.State != workspace.ComputeJobOrphaned || job.ErrorKind == nil || *job.ErrorKind != "namespace_recovery_unconfirmed" || computeNamespacePointer(job.ExternalID) != "actual-original-runtime" {
		t.Fatalf("job=%#v found=%t err=%v", job, found, err)
	}
	result, found, err := store.GetComputeJobResult(access.UserID, owned.Job.JobID)
	if err != nil || !found || result["output_manifest"] != prior["output_manifest"] || result["stdout_tail"] != prior["stdout_tail"] || result["checkpoint"] == nil || result["delivery_state"] != "held" || result["physical_outcome"] != "unconfirmed" || result["logical_task_complete"] != false {
		t.Fatalf("original receipt lost or false outcome=%#v found=%t err=%v", result, found, err)
	}
	notifications, err := store.ConsumeUnreadNotifications(ctx, access.Frame.ID, access.Frame.RootFrameID, access.UserID, 10)
	if err != nil || len(notifications) != 1 || notifications[0].Payload["state"] != workspace.ComputeJobOrphaned || notifications[0].Payload["logical_task_complete"] != false {
		t.Fatalf("notification=%#v err=%v", notifications, err)
	}
	projected, err := s.projectAgentKernelNotifications(ctx, access, notifications)
	if err != nil || projected["cells_completed"] != nil {
		t.Fatal("delivery hold fabricated physical completion", projected, err)
	}
	// A stale queued inventory must re-read the held state before provider
	// observation. There is intentionally no runner/runtime configured here.
	s.reconcileOwnedComputeJob(ctx, owned)
	var jobs, notices int
	if err := db.QueryRow(`SELECT COUNT(*) FROM compute_workbench_jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&notices); err != nil || jobs != 1 || notices != 1 {
		t.Fatal("held job duplicated or endlessly reprobed", jobs, notices, err)
	}
}

func TestNamespaceComputeHistoricalCheckpointResumePreservesActualBytes(t *testing.T) {
	exerciseNamespaceCheckpointResume(t, false)
}

func TestNamespaceComputePostCutoverObservedCheckpointResumesOffline(t *testing.T) {
	exerciseNamespaceCheckpointResume(t, true)
}

func exerciseNamespaceCheckpointResume(t *testing.T, firstObservedAfterCutover bool) {
	t.Helper()
	ctx := context.Background()
	store, _, db := newTranscriptWebFixture(t)
	s := &Server{workspaceStore: store, fileRoot: t.TempDir(), runtimeAssetsDir: filepath.Join(repositoryRootForServerTest(t), "assets/optional")}
	work := t.TempDir()
	contract := checkpoint.Contract{Manifest: "out/native.jsonl", ResumeCommand: "worker --resume out/state.bin", Signal: "TERM"}
	// Stage only bounded native fixture inputs; no compute worker is executed.
	seedTranscriptWebFrame(t, store, "staging-owner", "staging-project", "staging-frame")
	stagingAccess, _, err := store.GetKernelFrameAccess("staging-frame")
	if err != nil {
		t.Fatal(err)
	}
	jobID := "job-0123456789abcdef01234567"
	_, archive, err := s.stageAgentBYOCJob(jobID, work, map[string]any{"command": "worker", "checkpoint": contract}, stagingAccess)
	if err != nil {
		t.Fatal(err)
	}
	access, owned := seedServerNamespaceComputeJob(t, store, map[string]any{"archive_sha256": archive.SHA256, "workspace_dir": work, "checkpoint_contract": contract}, jobID)
	root := filepath.Join(work, "hpc", jobID)
	if err := os.MkdirAll(filepath.Join(root, "out"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("original committed checkpoint")
	if err := os.WriteFile(filepath.Join(root, "out/state.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	var source bytes.Buffer
	encoder := json.NewEncoder(&source)
	for _, record := range []any{checkpoint.Header{Schema: "synon.compute-checkpoint.v1", Generation: 2, SourceInputSHA256: archive.SHA256, ResumeCommandSHA256: contract.CommandSHA256()}, checkpoint.File{Path: "out/state.bin", SHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), Bytes: int64(len(payload))}, checkpoint.Commit{Commit: "complete", FileCount: 1, Bytes: int64(len(payload))}} {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(source.Bytes()))
	if err := os.WriteFile(filepath.Join(root, contract.Manifest), source.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt := checkpoint.Receipt{Header: checkpoint.Header{Schema: "synon.compute-checkpoint.v1", Generation: 2, SourceInputSHA256: archive.SHA256, ResumeCommandSHA256: contract.CommandSHA256()}, ManifestSHA256: digest, FileCount: 1, Bytes: int64(len(payload))}
	if !firstObservedAfterCutover {
		if err := store.SetComputeJobResult(access.UserID, jobID, map[string]any{"checkpoint": map[string]any{"contract": contract, "receipt": receipt}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.TransitionComputeJob(access.UserID, jobID, workspace.ComputeJobTimedOut, "timeout", time.Now()); err != nil {
		t.Fatal(err)
	}
	s.workspaceStore = captureServerNamespaceFixture(t, store, db)
	if firstObservedAfterCutover {
		observed, err := s.computeNamespaceContext(ctx, owned)
		if err != nil {
			t.Fatal(err)
		}
		stage := t.TempDir()
		transport := nativeHarvestTransport{root: root}
		if err := transport.Install(observed, filepath.Join(s.runtimeAssetsDir, "compute", "harvest.sh.tmpl")); err != nil {
			t.Fatal(err)
		}
		window, cancel := context.WithTimeout(observed, 2*time.Second)
		defer cancel()
		ready := false
		for !ready && window.Err() == nil {
			ready, err = fetchComputeCheckpointManifest(window, stage, mapValue(owned.Job.HardwareDetails), transport)
			if err != nil {
				t.Fatal("actual control download failed", err)
			}
			if !ready {
				time.Sleep(5 * time.Millisecond)
			}
		}
		if !ready {
			t.Fatal("actual control download did not finish", window.Err())
		}
		index, actualDigest, err := computeCheckpointSelection(observed, stage, mapValue(owned.Job.HardwareDetails))
		if err != nil || index == nil || actualDigest != digest {
			t.Fatal("verified checkpoint source not recorded", actualDigest, err)
		}
		_ = index.Close()
		if err := s.workspaceStore.SetComputeJobResult(access.UserID, jobID, map[string]any{"checkpoint": map[string]any{"contract": contract, "receipt": receipt}}); err != nil {
			t.Fatal(err)
		}
	}
	resume, err := s.computeCheckpointResume(ctx, access, map[string]any{"resume_from_job": jobID, "command": contract.ResumeCommand}, work)
	if err != nil || resume == nil || resume.NamespaceProof == nil || resume.Receipt != receipt {
		t.Fatalf("resume=%#v err=%v", resume, err)
	}
	actual, err := os.ReadFile(filepath.Join(root, contract.Manifest))
	if err != nil || !bytes.Equal(actual, source.Bytes()) {
		t.Fatal("historical source rewritten", err)
	}
	seen := 0
	for _, err := range computeInputRecords(ctx, map[string]any{}, resume, work) {
		if err != nil {
			t.Fatal(err)
		}
		seen++
	}
	if seen < 1 {
		t.Fatal("verified native checkpoint was not admitted as input")
	}
	if !computeHistoricalControlInputName("_synon_checkpoint.env", resume) || computeHistoricalControlInputName("out/synon-owned-user-file.bin", resume) || computeHistoricalControlInputName("_synon_checkpoint.env", nil) {
		t.Fatal("old control recipe omission escaped the verified resume boundary")
	}
	changed := receipt
	changed.ManifestSHA256 = strings.Repeat("d", 64)
	if err := s.workspaceStore.SetComputeJobResult(access.UserID, jobID, map[string]any{"checkpoint": map[string]any{"contract": contract, "receipt": changed}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.computeCheckpointResume(ctx, access, map[string]any{"resume_from_job": jobID, "command": contract.ResumeCommand}, work); !errors.Is(err, namespace.ErrRecoveryUnconfirmed) {
		t.Fatal("mutable current result minted historical source authority", err)
	}
	if _, _, err := s.harvestSelectedComputeOutputs(ctx, t.TempDir(), work, owned.Job.JobID, mapValue(owned.Job.HardwareDetails), func(path string) string { return path }, namespaceNoSideEffectTransport{}, workspace.OwnedComputeJob{OwnerUserID: "foreign", Job: owned.Job}); err == nil {
		t.Fatal("foreign job tried a provider operation")
	}
}

// A mismatched owner must be rejected before any provider operation.
type namespaceNoSideEffectTransport struct{}

func (namespaceNoSideEffectTransport) Install(context.Context, string) error {
	panic("unexpected provider operation")
}
func (namespaceNoSideEffectTransport) Control(context.Context, string, string, string) (transfer.ControlReceipt, error) {
	panic("unexpected provider operation")
}
func (namespaceNoSideEffectTransport) Select(context.Context, string, string, string) error {
	panic("unexpected provider operation")
}
func (namespaceNoSideEffectTransport) Fetch(context.Context, string, string, transfer.ControlReceipt) error {
	panic("unexpected provider operation")
}
