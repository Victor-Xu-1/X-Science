package transcript

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestTerminalProjectionOwnerDiscoveryDoesNotWaitForUnrelatedWriter(t *testing.T) {
	repository, writer, dsn := newTranscriptRepository(t)
	if _, err := writer.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	claim := seedArtifactProjectionClaim(t, repository, writer, "terminal-reader-stream", "terminal-reader-owner")
	event, _, _, err := repository.FinishRunner(t.Context(), FinishRunnerInput{
		Claim: claim, ClientMessageID: "terminal-reader-finished", Status: "completed",
		PayloadJSON: []byte(`{"status":"completed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`DELETE FROM transcript_delivery_intents WHERE stream_uid=? AND publication_seq=?`, claim.StreamUID, event.PublicationSeq); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", dsn+"&mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	var queryOnly int
	if err := reader.QueryRow("PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
		t.Fatalf("reader is not query-only: value=%d err=%v", queryOnly, err)
	}
	readRepository := NewRepositoryWithReadPool(writer, reader)
	writer.SetMaxOpenConns(1)
	held, err := writer.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		_ = held.Close()
		t.Fatal(err)
	}
	release := func() {
		_, _ = held.ExecContext(context.Background(), "ROLLBACK")
		_ = held.Close()
	}
	defer release()
	beforeWait := writer.Stats().WaitCount
	type ownerResult struct {
		owners []string
		err    error
	}
	done := make(chan ownerResult, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	go func() {
		owners, err := readRepository.ListTerminalProjectionOwners(ctx, 1)
		done <- ownerResult{owners, err}
	}()
	var result ownerResult
	for {
		select {
		case result = <-done:
			goto settled
		case <-ctx.Done():
			t.Error("terminal owner discovery waited for an unrelated writer")
			goto cleanup
		default:
		}
		if writer.Stats().WaitCount > beforeWait {
			t.Error("read-only terminal owner discovery entered the serialized writer pool")
			goto cleanup
		}
		runtime.Gosched()
	}
cleanup:
	release()
	result = <-done
settled:
	release()
	if result.err != nil || len(result.owners) != 1 || result.owners[0] != claim.OwnerID {
		t.Fatalf("canonical missing-terminal owner=%v err=%v", result.owners, result.err)
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := readRepository.ListTerminalProjectionOwners(canceled, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("reader lost caller cancellation: %v", err)
	}
	if count, err := repository.RecoverTerminalDeliveryIntents(t.Context(), claim.OwnerID); err != nil || count != 1 {
		t.Fatalf("original recovery owner count=%d err=%v", count, err)
	}
	if owners, err := readRepository.ListTerminalProjectionOwners(t.Context(), 1); err != nil || len(owners) != 0 {
		t.Fatalf("settled terminal still pending: owners=%v err=%v", owners, err)
	}
}
