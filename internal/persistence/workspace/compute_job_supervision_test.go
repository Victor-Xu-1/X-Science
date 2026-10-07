package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func supervisedJobFixture(t *testing.T) (*Store, ComputeJob) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.SetBYOCEnabled("modal", "owner", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	j, err := s.CreateComputeJob("owner", ComputeJob{JobID: "job-supervised", ProjectID: "project", Provider: "byoc:modal", Environment: "remote", TierType: "remote", ProviderFamily: "byoc"})
	if err != nil {
		t.Fatal(err)
	}
	j, err = s.BindComputeJobExternal("owner", j.JobID, "original-external-id", "")
	if err != nil {
		t.Fatal(err)
	}
	return s, j
}

func TestComputeControlObservationPreservesOwnershipAndExecutionState(t *testing.T) {
	s, j := supervisedJobFixture(t)
	ctx := context.Background()
	if err := s.SetComputeJobControlUnavailable(ctx, "other", j.JobID, "control_unreachable"); !errors.Is(err, ErrComputeJobNotFound) {
		t.Fatalf("wrong owner: %v", err)
	}
	if err := s.SetComputeJobControlUnavailable(ctx, "owner", j.JobID, "control_unreachable"); err != nil {
		t.Fatal(err)
	}
	current, _, err := s.GetComputeJob("owner", j.JobID)
	if err != nil || current.State != ComputeJobRunning || current.EndedAtISO != nil || current.ExternalID == nil || *current.ExternalID != "original-external-id" || current.ErrorKind == nil || *current.ErrorKind != "control_unreachable" {
		t.Fatalf("observation changed execution: %#v %v", current, err)
	}
	if err := s.ClearComputeJobControlUnavailable(ctx, "owner", j.JobID); err != nil {
		t.Fatal(err)
	}
	current, _, err = s.GetComputeJob("owner", j.JobID)
	if err != nil || current.ErrorKind != nil || current.SystemHint != nil {
		t.Fatalf("recovery not cleared: %#v %v", current, err)
	}
	if _, err := s.TransitionComputeJob("owner", j.JobID, ComputeJobFailed, "cancelled", s.now()); err != nil {
		t.Fatal(err)
	}
	terminal, _, err := s.GetComputeJob("owner", j.JobID)
	if err != nil || terminal.SystemHint != nil {
		t.Fatalf("terminal receipt retained an unknown-control hint: %#v %v", terminal, err)
	}
	if err := s.SetComputeJobControlUnavailable(ctx, "owner", j.JobID, "control_unreachable"); !errors.Is(err, ErrComputeJobTransition) {
		t.Fatalf("late probe revived terminal job: %v", err)
	}
	if err := s.ClearComputeJobControlUnavailable(ctx, "owner", j.JobID); !errors.Is(err, ErrComputeJobTransition) {
		t.Fatalf("late clear erased terminal cause: %v", err)
	}
}

func TestComputeSupervisorPagesPastOneThousandJobs(t *testing.T) {
	s, j := supervisedJobFixture(t)
	// Exercise actual SQLite keyset pagination; creation/admission is tested by
	// its own contract. Equal timestamps force the job-id tie breaker.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for n := 0; n < 1005; n++ {
		family := "ssh"
		if n%2 == 0 {
			family = "byoc"
		}
		state := ComputeJobRunning
		if n%3 == 0 {
			state = ComputeJobQueued
		}
		if _, err := tx.Exec(`INSERT INTO compute_workbench_jobs(job_id,owner_user_id,project_id,environment,tier_type,provider,state,started_at,provider_family,provider_label,intent_json,hardware_json)
			VALUES(?,?,?,?,?,?,?,?,?,?,'null','{}')`, fmt.Sprintf("job-page-%04d", n), "owner", "project", "remote", "remote", "fixture", state, j.StartedAt, family, family); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := s.ListSupervisedComputeJobsPage(t.Context(), cursor, 137)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Jobs) > 137 {
			t.Fatal("unbounded query")
		}
		for _, item := range page.Jobs {
			if seen[item.Job.JobID] || item.OwnerUserID != "owner" {
				t.Fatalf("duplicate or lost owner: %#v", item)
			}
			seen[item.Job.JobID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 1006 {
		t.Fatalf("inventory truncated: %d", len(seen))
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ListSupervisedComputeJobsPage(cancelled, "", 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled page query: %v", err)
	}
}
