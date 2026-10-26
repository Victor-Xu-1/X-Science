package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func attachmentInitReceiptFixture(t *testing.T) (*Store, InitAttachmentUploadInput, AttachmentUploadInitPolicy) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "project-init", UserID: "owner-init", Name: "Upload admission"}); err != nil {
		t.Fatal(err)
	}
	return store, InitAttachmentUploadInput{UserID: "owner-init", ProjectID: "project-init", Filename: "admission.txt", ContentType: "text/plain", TotalSize: 4}, AttachmentUploadInitPolicy{DefaultChunkSize: 4, MinChunkSize: 1, MaxChunkSize: 8}
}

func attachmentInitReceiptContext(t *testing.T, key string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return WithMutationIdempotencyKey(ctx, key)
}

func TestAttachmentInitExplicitKeyReplaysAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "project-init", UserID: "owner-init", Name: "Upload admission"}); err != nil {
		t.Fatal(err)
	}
	const operationID = "5a95b389-2e9a-4c88-aa74-335e10fbba55"
	ctx := attachmentInitReceiptContext(t, operationID)
	input := InitAttachmentUploadInput{UserID: "owner-init", ProjectID: "project-init", Filename: "admission.txt", ContentType: "text/plain", TotalSize: 4, ChunkSize: 4}
	policy := AttachmentUploadInitPolicy{DefaultChunkSize: 4, MinChunkSize: 1, MaxChunkSize: 8}
	first, err := store.InitAttachmentUploadWithReceipt(ctx, operationID, input, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.InitAttachmentUploadWithReceipt(ctx, operationID, input, policy)
	if err != nil {
		t.Fatal(err)
	}
	var rows, receipts int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachment_uploads WHERE user_id = ? AND project_id = ?`, input.UserID, input.ProjectID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mutation_result_ledger WHERE owner_user_id=? AND operation=?`, input.UserID, attachmentInitOperation).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if first != replay || rows != 1 || receipts != 1 {
		t.Fatalf("same operation did not replay one admission: first=%+v replay=%+v rows=%d receipts=%d", first, replay, rows, receipts)
	}
	var raw string
	if err := store.db.QueryRow(`SELECT result_json FROM mutation_result_ledger WHERE operation=?`, attachmentInitOperation).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || len(fields) != 3 || fields["upload_id"] != first.UploadID || fields["chunk_size"] != float64(4) || fields["total_chunks"] != float64(1) {
		t.Fatalf("receipt is not the immutable three-field ACK: %s err=%v", raw, err)
	}
}

func TestAttachmentInitReceiptPrecedesPolicyAndUploadLifecycle(t *testing.T) {
	for _, lifecycle := range []string{"cancel", "expire", "finalize"} {
		t.Run(lifecycle, func(t *testing.T) {
			store, input, policy := attachmentInitReceiptFixture(t)
			const operationID = "4af33af5-9526-4137-a6d2-0e22a6ae9c32"
			ctx := attachmentInitReceiptContext(t, operationID)
			first, err := store.InitAttachmentUploadWithReceipt(ctx, operationID, input, policy)
			if err != nil {
				t.Fatal(err)
			}
			switch lifecycle {
			case "cancel":
				err = store.CancelAttachmentUpload(t.Context(), input.UserID, first.UploadID)
			case "expire":
				later := store.now().UTC().Add(attachmentUploadReservationTTL + time.Minute)
				store.now = func() time.Time { return later }
				_, _, err = store.GetAttachmentUpload(t.Context(), input.UserID, first.UploadID)
			case "finalize":
				if _, err = store.SaveAttachmentChunk(t.Context(), input.UserID, first.UploadID, 0, bytes.NewBufferString("data")); err == nil {
					_, err = store.FinalizeAttachmentUpload(ctx, input.UserID, first.UploadID, "")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			// The original zero request means its original default, even if the
			// current supplied policy has changed or is no longer valid.
			replay, err := store.InitAttachmentUploadWithReceipt(ctx, operationID, input, AttachmentUploadInitPolicy{})
			if err != nil || replay != first {
				t.Fatalf("historical ACK lost before mutable policy: first=%+v replay=%+v err=%v", first, replay, err)
			}
			changed := input
			changed.ChunkSize = first.ChunkSize
			if _, err := store.InitAttachmentUploadWithReceipt(ctx, operationID, changed, policy); !errors.Is(err, ErrMutationIdempotencyConflict) {
				t.Fatalf("explicit chunk size was conflated with raw default zero: %v", err)
			}
			if _, found, err := store.GetAttachmentUpload(t.Context(), input.UserID, first.UploadID); err != nil || found {
				t.Fatalf("ACK replay recreated an inactive upload: found=%t err=%v", found, err)
			}
			var rows int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM attachment_uploads`).Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("ACK replay allocated replacement: rows=%d err=%v", rows, err)
			}
		})
	}
}

func TestAttachmentInitReceiptSerializesKeyAndFencesOwners(t *testing.T) {
	store, input, policy := attachmentInitReceiptFixture(t)
	var databasePath string
	if err := store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&databasePath); err != nil {
		t.Fatal(err)
	}
	secondStore, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.Close() })
	const operationID = "7a767c8c-edab-49d2-bf41-18f8c5b6508a"
	ctx := attachmentInitReceiptContext(t, operationID)
	const callers = 6
	acks := make(chan AttachmentUploadInitReceipt, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			target := store
			if index%2 != 0 {
				target = secondStore
			}
			ack, err := target.InitAttachmentUploadWithReceipt(ctx, operationID, input, policy)
			acks <- ack
			errs <- err
		}(index)
	}
	wg.Wait()
	close(acks)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("same-key concurrency failed instead of replaying: %v", err)
		}
	}
	var first AttachmentUploadInitReceipt
	for ack := range acks {
		if first.UploadID == "" {
			first = ack
		}
		if ack != first {
			t.Fatalf("same key allocated separate ACKs: first=%+v ack=%+v", first, ack)
		}
	}
	changed := input
	changed.Filename = "different.txt"
	if _, err := store.InitAttachmentUploadWithReceipt(ctx, operationID, changed, policy); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed payload did not conflict: %v", err)
	}
	foreign := input
	foreign.UserID = "other-owner"
	if _, err := store.InitAttachmentUploadWithReceipt(ctx, operationID, foreign, policy); !errors.Is(err, ErrAttachmentInitWriteRejected) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign project admission was not rejected: %v", err)
	}
	if _, err := store.CreateProject(CreateProjectInput{ID: "other-project", UserID: foreign.UserID, Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	foreign.ProjectID = "other-project"
	other, err := store.InitAttachmentUploadWithReceipt(ctx, operationID, foreign, policy)
	if err != nil || other.UploadID == first.UploadID {
		t.Fatalf("different owner shared receipt identity: %+v err=%v", other, err)
	}
	var rows, receipts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM attachment_uploads`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE operation=?`, attachmentInitOperation).Scan(&receipts); err != nil || rows != 2 || receipts != 2 {
		t.Fatalf("concurrent/owner admission counts rows=%d receipts=%d err=%v", rows, receipts, err)
	}
}

func TestAttachmentInitReceiptRejectsAbsentAndNoncanonicalIdentity(t *testing.T) {
	store, input, policy := attachmentInitReceiptFixture(t)
	const operationID = "b51b1b06-810a-4313-b173-7cd6779bc515"
	for _, test := range []struct {
		name        string
		ctx         context.Context
		operationID string
	}{
		{name: "nil context", ctx: nil, operationID: operationID},
		{name: "missing caller ID", ctx: t.Context(), operationID: ""},
		{name: "invalid caller ID", ctx: t.Context(), operationID: "not-a-uuid"},
		{name: "noncanonical caller ID", ctx: t.Context(), operationID: strings.ToUpper(operationID)},
		{name: "invalid context identity", ctx: WithMutationIdempotencyKey(t.Context(), "not-a-uuid"), operationID: operationID},
		{name: "different context identity", ctx: WithMutationIdempotencyKey(t.Context(), "5a95b389-2e9a-4c88-aa74-335e10fbba55"), operationID: operationID},
		{name: "invalid context value", ctx: context.WithValue(t.Context(), mutationIdempotencyContextKey{}, "invalid"), operationID: operationID},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.InitAttachmentUploadWithReceipt(test.ctx, test.operationID, input, policy); !errors.Is(err, ErrAttachmentInitWriteRejected) || errors.Is(err, ErrAttachmentInitOutcomeUnknown) {
				t.Fatalf("missing/noncanonical identity was not rejected before writes: %v", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(attachmentInitReceiptContext(t, operationID))
	cancel()
	if _, err := store.InitAttachmentUploadWithReceipt(canceled, operationID, input, policy); !errors.Is(err, ErrAttachmentInitOutcomeUnknown) || errors.Is(err, ErrAttachmentInitWriteRejected) {
		t.Fatalf("canceled read was treated as noncommit proof: %v", err)
	}
	var rows, receipts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM attachment_uploads`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("identity validation wrote admission: rows=%d err=%v", rows, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE operation=?`, attachmentInitOperation).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("identity validation wrote receipt: count=%d err=%v", receipts, err)
	}
	// An otherwise unkeyed context is valid only with the explicit caller ID.
	ack, err := store.InitAttachmentUploadWithReceipt(t.Context(), operationID, input, policy)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.InitAttachmentUploadWithReceipt(WithMutationIdempotencyKey(t.Context(), operationID), operationID, input, policy)
	if err != nil || replayed != ack {
		t.Fatalf("explicit binding did not reuse the canonical context owner: %+v err=%v", replayed, err)
	}
}

func TestAttachmentInitReceiptRollsBackRowsAndKeepsUnknownTruthful(t *testing.T) {
	t.Run("statement rollback and exact readback", func(t *testing.T) {
		store, input, policy := attachmentInitReceiptFixture(t)
		const key = "8f287b3f-c2a3-4210-90a0-622e117ee0d3"
		ctx := attachmentInitReceiptContext(t, key)
		if _, err := store.db.Exec(`CREATE TRIGGER reject_init_receipt BEFORE INSERT ON mutation_result_ledger
            WHEN NEW.operation='artifact.upload.init' BEGIN SELECT RAISE(ABORT,'not found unique synthetic SQL failure'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.InitAttachmentUploadWithReceipt(ctx, key, input, policy); !errors.Is(err, ErrAttachmentInitOutcomeUnknown) || errors.Is(err, ErrAttachmentInitWriteRejected) {
			t.Fatalf("SQL failure invented prewrite rejection: %v", err)
		}
		var rows, receipts int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM attachment_uploads`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE operation=?`, attachmentInitOperation).Scan(&receipts); err != nil || rows != 0 || receipts != 0 {
			t.Fatalf("row and ACK were not atomic: rows=%d receipts=%d err=%v", rows, receipts, err)
		}
		entries, err := os.ReadDir(filepath.Join(store.blobRoot, "uploads"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("proven rollback retained own empty directory: %d err=%v", len(entries), err)
		}
		if _, err := store.db.Exec(`DROP TRIGGER reject_init_receipt`); err != nil {
			t.Fatal(err)
		}
		ack, err := store.InitAttachmentUploadWithReceipt(ctx, key, input, policy)
		if err != nil {
			t.Fatal(err)
		}
		identity, err := attachmentInitRequestIdentity(ctx, key, input)
		if err != nil {
			t.Fatal(err)
		}
		recovered, err := store.resolveAttachmentInitFailure(ctx, identity, errors.New("synthetic lost commit acknowledgement"))
		if err != nil || recovered != ack {
			t.Fatalf("exact stored ACK was not recovered: %+v err=%v", recovered, err)
		}
		if _, err := store.db.Exec(`UPDATE mutation_result_ledger SET result_json='broken-json' WHERE operation=?`, attachmentInitOperation); err != nil {
			t.Fatal(err)
		}
		if _, err := store.InitAttachmentUploadWithReceipt(ctx, key, input, policy); !errors.Is(err, ErrAttachmentInitOutcomeUnknown) {
			t.Fatalf("invalid persisted ACK caused replacement or rejection: %v", err)
		}
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM attachment_uploads`).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("readback allocated a second upload: rows=%d err=%v", rows, err)
		}
	})
	t.Run("commit error retains directory until original startup recovery", func(t *testing.T) {
		store, input, policy := attachmentInitReceiptFixture(t)
		var databasePath string
		if err := store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&databasePath); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`CREATE TABLE init_commit_probe(project_id TEXT REFERENCES projects(id) DEFERRABLE INITIALLY DEFERRED);
            CREATE TRIGGER fail_init_commit AFTER INSERT ON mutation_result_ledger
            WHEN NEW.operation='artifact.upload.init' BEGIN INSERT INTO init_commit_probe VALUES('missing-project'); END`); err != nil {
			t.Fatal(err)
		}
		const key = "48886cf0-c4fb-4798-916c-d3b585948d81"
		ctx := attachmentInitReceiptContext(t, key)
		if _, err := store.InitAttachmentUploadWithReceipt(ctx, key, input, policy); !errors.Is(err, ErrAttachmentInitOutcomeUnknown) || errors.Is(err, ErrAttachmentInitWriteRejected) {
			t.Fatalf("Commit failure did not remain unknown: %v", err)
		}
		entries, err := os.ReadDir(filepath.Join(store.blobRoot, "uploads"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("Commit-attempt directory was removed before proof: count=%d err=%v", len(entries), err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		entries, err = os.ReadDir(filepath.Join(reopened.blobRoot, "uploads"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("existing startup recovery did not reclaim orphan: count=%d err=%v", len(entries), err)
		}
	})
}

func TestAttachmentLegacyInitKeepsFreshAdmissionSemantics(t *testing.T) {
	store, input, _ := attachmentInitReceiptFixture(t)
	input.ChunkSize = 4
	ctx := attachmentInitReceiptContext(t, "d5b10a9d-7dc8-4239-9c46-359f69620f50")
	first, err := store.InitAttachmentUpload(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.InitAttachmentUpload(ctx, input)
	if err != nil || first.ID == second.ID || !reflect.DeepEqual(first.ReceivedChunks, []int{}) {
		t.Fatalf("legacy fresh init changed: first=%+v second=%+v err=%v", first, second, err)
	}
	input.Filename = "../bad.txt"
	if _, err := store.InitAttachmentUpload(ctx, input); err == nil || !strings.Contains(err.Error(), "filename") {
		t.Fatalf("legacy validation changed: %v", err)
	}
}

func TestAttachmentInitReceiptRequiresCallerOperationIdentity(t *testing.T) {
	store, input, policy := attachmentInitReceiptFixture(t)
	ctx := WithMutationIdempotencyKey(t.Context(), "")
	_, err := store.InitAttachmentUploadWithReceipt(ctx, "", input, policy)
	var rows, receipts int
	if scanErr := store.db.QueryRow(`SELECT COUNT(*) FROM attachment_uploads`).Scan(&rows); scanErr != nil {
		t.Fatal(scanErr)
	}
	if scanErr := store.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE operation=?`, attachmentInitOperation).Scan(&receipts); scanErr != nil {
		t.Fatal(scanErr)
	}
	if !errors.Is(err, ErrAttachmentInitWriteRejected) || rows != 0 || receipts != 0 {
		t.Fatalf("generated context UUID was accepted as caller identity: rejected=%t rows=%d receipts=%d", errors.Is(err, ErrAttachmentInitWriteRejected), rows, receipts)
	}
}
