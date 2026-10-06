package server

import (
	"context"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func TestComputeJobActorSerializesSubmissionAndReconciliation(t *testing.T) {
	s := &Server{}
	if !s.claimComputeJobActor("owner", "job") {
		t.Fatal("first submitter did not acquire the actor")
	}
	if s.claimComputeJobActor("owner", "job") {
		t.Fatal("same job obtained concurrent authority")
	}
	// This must not reach a workspace/provider operation while submission owns
	// the job. A separate unrelated job remains independently admissible.
	s.reconcileOwnedComputeJob(context.Background(), workspace.OwnedComputeJob{OwnerUserID: "owner", Job: workspace.ComputeJob{JobID: "job"}})
	if !s.claimComputeJobActor("owner", "other-job") {
		t.Fatal("unrelated job was globally serialized")
	}
	s.releaseComputeJobActor("owner", "other-job")
	s.releaseComputeJobActor("owner", "job")
	if !s.claimComputeJobActor("owner", "job") {
		t.Fatal("settled submitter did not release reconciliation")
	}
	s.releaseComputeJobActor("owner", "job")
}
