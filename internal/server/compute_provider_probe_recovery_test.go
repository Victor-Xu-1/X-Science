package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	kernelruntime "synon-go/internal/kernel"
	"synon-go/internal/persistence/runtimekv"
	workspace "synon-go/internal/persistence/workspace"
)

func TestComputeProbeRecoveryRetainsRemoteIdentityAcrossReconstruction(t *testing.T) {
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.SetBYOCEnabled("modal", "owner", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	j, err := store.CreateComputeJob("owner", workspace.ComputeJob{JobID: "job-probe-recovery", ProjectID: "project", Provider: "byoc:modal", Environment: "remote", TierType: "remote", ProviderFamily: "byoc"})
	if err != nil {
		t.Fatal(err)
	}
	j, err = store.BindComputeJobExternal("owner", j.JobID, "original-sandbox", "")
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "runtime.sqlite")
	state := runtimekv.New(statePath)
	s := &Server{workspaceStore: store, runtimeStore: state}
	owned := workspace.OwnedComputeJob{OwnerUserID: "owner", Job: j}
	for range 6 {
		if s.handleComputeProviderProbeFailure(owned, &kernelruntime.ProviderOperationError{Kind: "transient", Message: "temporary network failure"}) {
			t.Fatal("transient network failure became terminal")
		}
	}
	current, _, err := store.GetComputeJob("owner", j.JobID)
	if err != nil || current.State != workspace.ComputeJobRunning || current.EndedAtISO != nil || current.ExternalID == nil || *current.ExternalID != "original-sandbox" {
		t.Fatalf("remote identity lost: %#v %v", current, err)
	}
	projection := kernelComputeJobProjection(current)
	if projection["execution_outcome"] != "unknown" || projection["control_status"] != "control_unreachable" {
		t.Fatalf("false live claim: %#v", projection)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := runtimekv.New(statePath)
	t.Cleanup(func() { _ = reopened.Close() })
	s.runtimeStore = reopened
	if s.computeProviderProbeDue(j.JobID) {
		t.Fatal("reconstruction erased durable backoff")
	}
	entry, found, err := reopened.Get(computeProviderProbeNamespace, j.JobID)
	if err != nil || !found || numberValue(mapValue(entry.Value)["count"]) != 6 {
		t.Fatalf("failure observation lost: %#v %v", entry, err)
	}
	if computeProviderProbeDelay(10000) != 5*time.Minute {
		t.Fatal("backoff did not saturate")
	}
	if err := s.resetOwnedComputeProviderProbe(owned); err != nil {
		t.Fatal(err)
	}
	if !s.computeProviderProbeDue(j.JobID) {
		t.Fatal("healthy recovery did not release observation delay")
	}
	if s.handleComputeProviderProbeFailure(owned, &kernelruntime.ProviderOperationError{Kind: "not_found", Message: "authoritative provider inventory confirms missing job"}) != true {
		t.Fatal("definitive disappearance was hidden")
	}
	current, _, err = store.GetComputeJob("owner", j.JobID)
	if err != nil || current.State != workspace.ComputeJobOrphaned || current.EndedAtISO == nil {
		t.Fatalf("definitive loss not terminal: %#v %v", current, err)
	}
	if err := store.ClearComputeJobControlUnavailable(context.Background(), "owner", j.JobID); err == nil {
		t.Fatal("late recovery cleared terminal loss")
	}
}
