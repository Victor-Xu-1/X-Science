package server

import (
	"context"
	"database/sql"
	"errors"
	transcriptstore "synon-go/internal/persistence/transcript"
	"testing"
	"time"
)

func TestRunnerLeaseRenewalRecoversRealLockWithoutMutatingClaim(t *testing.T) {
	_, repo, db := newTranscriptWebFixture(t)
	ctx := context.Background()
	if _, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{UID: "lease-test", OwnerID: "owner", ExternalID: "lease-test", Kind: transcriptstore.StreamKindStandalone, Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.AppendUserEvent(ctx, transcriptstore.AppendUserEventInput{StreamUID: "lease-test", OwnerID: "owner", ClientMessageID: "input", PayloadJSON: []byte(`{"text":"work"}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{StreamUID: "lease-test", OwnerID: "owner", RunnerID: "worker", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim: %v", err)
	}
	// Discover the fixture's real main database, not a mocked persistence error.
	var seq int
	var name, path string
	if err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	blocker, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	conn, err := blocker.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, "ROLLBACK")
	done := make(chan error, 1)
	app := &Server{transcriptStore: repo}
	go func() {
		result, err := app.renewTranscriptRunnerLease(ctx, claimed.Claim, time.Minute, claimed.Claim.ExpiresAt)
		if err == nil && !result.Renewed {
			err = errors.New("not renewed")
		}
		done <- err
	}()
	time.Sleep(75 * time.Millisecond)
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("renewal did not recover")
	}
	expired := time.Now().Add(-time.Second)
	if _, err := app.renewTranscriptRunnerLease(ctx, claimed.Claim, time.Minute, expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired local lease reused: %v", err)
	}
}

func TestRunnerLeaseRenewalDoesNotAbandonLiveGrantOnWriterContention(t *testing.T) {
	_, repo, db := newTranscriptWebFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	stream, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "lease-writer-contention", OwnerID: "owner", ExternalID: "lease-writer-contention",
		Kind: transcriptstore.StreamKindStandalone, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.AppendUserEvent(ctx, transcriptstore.AppendUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "lease-writer-input",
		PayloadJSON: []byte(`{"text":"continue the already authorized work"}`),
	}); err != nil {
		t.Fatal(err)
	}
	const ttl = 9 * time.Second
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "lease-writer-runner",
		TTL: ttl, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	occupied, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if _, err := occupied.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer occupied.ExecContext(context.Background(), "ROLLBACK")
	type outcome struct {
		result transcriptstore.HeartbeatRunnerResult
		err    error
	}
	done := make(chan outcome, 1)
	app := &Server{transcriptStore: repo}
	go func() {
		result, err := app.renewTranscriptRunnerLease(ctx, claimed.Claim, ttl, claimed.Claim.ExpiresAt)
		done <- outcome{result: result, err: err}
	}()
	// The writer becomes available after the old ttl/3 slice, but well inside
	// the real grant. No fixture database, caller deadline or lease is extended.
	release := time.NewTimer(4 * time.Second)
	defer release.Stop()
	select {
	case <-release.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := occupied.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || !result.result.Renewed {
			t.Fatalf("renewal abandoned a still-live grant: renewed=%t err=%v", result.result.Renewed, result.err)
		}
		if !result.result.ExpiresAt.After(claimed.Claim.ExpiresAt) {
			t.Fatal("real renewal did not extend the durable lease")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	state, err := repo.GetRunnerRuntimeState(ctx, stream.UID, stream.OwnerID, claimed.Claim.Attempt)
	if err != nil || state.Status != "running" || !state.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("renewed authority=%#v err=%v", state, err)
	}
}
