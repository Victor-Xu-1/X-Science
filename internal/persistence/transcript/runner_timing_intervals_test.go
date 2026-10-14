package transcript

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestRunnerTimingExcludesPausedIntervalsAcrossResumeAndNewInput(t *testing.T) {
	for _, mode := range []string{"same-attempt", "new-input"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunnerTimingFixture(t)
			f.now = f.now.Add(90 * time.Second)
			checkpoint := f.pause(t)
			f.now = f.now.Add(24 * time.Hour)
			f.check(t, 90*time.Second, 90*time.Second, false)
			if mode == "new-input" {
				f.newInput(t)
			} else {
				f.resume(t, checkpoint)
			}
			f.now = f.now.Add(30 * time.Second)
			latest := 120 * time.Second
			if mode == "new-input" {
				latest = 30 * time.Second
			}
			f.check(t, latest, 120*time.Second, true)
		})
	}
}

func TestRunnerTimingRepeatedPausesSurviveReopenAndTerminalSettlement(t *testing.T) {
	f := newRunnerTimingFixture(t)
	for i := range 3 {
		f.now = f.now.Add(20 * time.Second)
		checkpoint := f.pause(t)
		f.now = f.now.Add(48 * time.Hour)
		f.reopen(t)
		f.check(t, time.Duration(i+1)*20*time.Second, time.Duration(i+1)*20*time.Second, false)
		f.resume(t, checkpoint)
		f.check(t, time.Duration(i+1)*20*time.Second, time.Duration(i+1)*20*time.Second, true)
	}
	f.now = f.now.Add(30 * time.Second)
	if _, _, created, err := f.repo.FinishRunner(context.Background(), FinishRunnerInput{
		Claim: f.claim, ClientMessageID: "timing-finished", Status: "completed",
		PayloadJSON: []byte(`{"summary":"done"}`), Destinations: []string{"ws"},
	}); err != nil || !created {
		t.Fatalf("finish created=%t err=%v", created, err)
	}
	f.now = f.now.Add(24 * time.Hour)
	f.reopen(t)
	f.check(t, 90*time.Second, 90*time.Second, false)
}

type runnerTimingFixture struct {
	repo  *Repository
	db    *sql.DB
	dsn   string
	now   time.Time
	claim RunnerClaim
	steps int
}

func newRunnerTimingFixture(t *testing.T) *runnerTimingFixture {
	t.Helper()
	repo, db, dsn := newTranscriptRepository(t)
	f := &runnerTimingFixture{repo: repo, db: db, dsn: dsn, now: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)}
	repo.now = func() time.Time { return f.now }
	seedTranscriptInput(t, repo, "timing-intervals", "owner-a")
	f.claimRunner(t, ResumeSourceFresh, 0)
	return f
}

func (f *runnerTimingFixture) claimRunner(t *testing.T, source ResumeSource, checkpoint int64) {
	t.Helper()
	f.steps++
	claimed, err := f.repo.ClaimRunner(context.Background(), ClaimRunnerInput{
		StreamUID: "timing-intervals", OwnerID: "owner-a", RunnerID: fmt.Sprintf("runner-%d", f.steps),
		TTL: time.Hour, ResumeSource: source, ResumeCheckpoint: checkpoint,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	f.claim = claimed.Claim
}

func (f *runnerTimingFixture) pause(t *testing.T) int64 {
	t.Helper()
	paused, err := f.repo.InterruptRunner(context.Background(), InterruptRunnerInput{
		Claim: f.claim, ClientMessageID: fmt.Sprintf("pause-%d", f.steps), ReasonCode: "runtime_draining",
		Resumable: true, AutoResume: true, Destinations: []string{"ws"},
	})
	if err != nil || !paused.Created {
		t.Fatalf("pause=%+v err=%v", paused, err)
	}
	return paused.Checkpoint.Sequence
}

func (f *runnerTimingFixture) resume(t *testing.T, checkpoint int64) {
	t.Helper()
	previous := f.claim
	f.claimRunner(t, ResumeSourceCheckpoint, checkpoint)
	if f.claim.Attempt != previous.Attempt || f.claim.ClaimToken == previous.ClaimToken {
		t.Fatalf("resume changed parent or retained old authority: before=%+v after=%+v", previous, f.claim)
	}
}

func (f *runnerTimingFixture) newInput(t *testing.T) {
	t.Helper()
	if _, created, err := f.repo.AppendUserEvent(context.Background(), AppendUserEventInput{
		StreamUID: "timing-intervals", OwnerID: "owner-a", ClientMessageID: "next-input",
		PayloadJSON: []byte(`{"text":"next task round"}`), Destinations: []string{"ws"},
	}); err != nil || !created {
		t.Fatalf("next input created=%t err=%v", created, err)
	}
	f.claimRunner(t, ResumeSourceFresh, 0)
}

func (f *runnerTimingFixture) reopen(t *testing.T) {
	t.Helper()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f.db, f.repo = db, NewRepository(db)
	f.repo.now = func() time.Time { return f.now }
}

func (f *runnerTimingFixture) check(t *testing.T, latestElapsed, taskElapsed time.Duration, active bool) {
	t.Helper()
	latest, found, err := f.repo.GetLatestRunnerTimingSummary(context.Background(), "timing-intervals", "owner-a")
	if err != nil || !found || latest.Elapsed != latestElapsed || latest.Active != active {
		t.Errorf("latest=%+v found=%t err=%v, want elapsed=%s active=%t", latest, found, err, latestElapsed, active)
	}
	task, found, err := f.repo.GetRunnerTaskTimingSummary(context.Background(), "timing-intervals", "owner-a")
	if err != nil || !found || task.Elapsed != taskElapsed || task.Active != active {
		t.Errorf("task=%+v found=%t err=%v, want elapsed=%s active=%t", task, found, err, taskElapsed, active)
	}
}
