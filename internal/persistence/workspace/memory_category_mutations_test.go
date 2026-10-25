package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func categoryReceiptFixture(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
func categoryReceiptContext(t *testing.T, key string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return WithMutationIdempotencyKey(ctx, key)
}

func TestMemoryCategoryReceiptsPreserveLaterStateAndDeletedResult(t *testing.T) {
	store := categoryReceiptFixture(t)
	const owner = "category-owner"
	createCtx := categoryReceiptContext(t, "original-create")
	original, err := store.CreateMemoryCategory(createCtx, owner, "Original", "Original guidance", true)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index < maxMemoryCategoriesPerUser; index++ {
		if _, err := store.CreateMemoryCategory(categoryReceiptContext(t, fmt.Sprintf("fill-%d", index)), owner, fmt.Sprintf("Category %d", index), "Fill routing guidance", true); err != nil {
			t.Fatal(err)
		}
	}
	// Receipt precedes mutable capacity and uniqueness checks.
	if replay, err := store.CreateMemoryCategory(createCtx, owner, "Original", "Original guidance", true); err != nil || replay.ID != original.ID {
		t.Fatalf("at-cap replay failed: id=%q err=%v", replay.ID, err)
	}
	if _, err := store.CreateMemoryCategory(createCtx, owner, "Different", "Original guidance", true); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed request did not conflict before cap check: %v", err)
	}
	name := "Renamed"
	updateCtx := categoryReceiptContext(t, "original-update")
	if _, err := store.UpdateMemoryCategory(updateCtx, owner, original.ID, UpdateMemoryCategoryInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	guidance, autoRecall := "Later guidance", false
	latest, err := store.UpdateMemoryCategory(categoryReceiptContext(t, "later-update"), owner, original.ID, UpdateMemoryCategoryInput{Guidance: &guidance, AutoRecall: &autoRecall})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.UpdateMemoryCategory(updateCtx, owner, original.ID, UpdateMemoryCategoryInput{Name: &name})
	if err != nil || replay.Guidance != guidance || replay.AutoRecall || !replay.UpdatedAt.Equal(latest.UpdatedAt) {
		t.Fatalf("old partial update overwrote later state: %+v err=%v", replay, err)
	}
	// Presence is part of the original typed request, even if a supplied value
	// currently equals the later database row.
	if _, err := store.UpdateMemoryCategory(updateCtx, owner, original.ID, UpdateMemoryCategoryInput{Name: &name, Guidance: &guidance}); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("field-presence drift accepted: %v", err)
	}
	replay, err = store.CreateMemoryCategory(createCtx, owner, "Original", "Original guidance", true)
	if err != nil || replay.ID != original.ID || replay.Name != name || replay.Guidance != guidance || !replay.UpdatedAt.Equal(latest.UpdatedAt) {
		t.Fatalf("create receipt did not read current result: %+v err=%v", replay, err)
	}
	other, err := store.CreateMemoryCategory(createCtx, "other-owner", "Original", "Original guidance", true)
	if err != nil || other.ID == original.ID || other.UserID != "other-owner" {
		t.Fatalf("owner/key namespaces collided: %+v err=%v", other, err)
	}
	if _, err := store.UpdateMemoryCategory(categoryReceiptContext(t, "foreign-update"), "other-owner", original.ID, UpdateMemoryCategoryInput{Name: &name}); !errors.Is(err, ErrMemoryCategoryWriteRejected) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-owner update was not a proven404: %v", err)
	}
	if _, err := store.DeleteMemoryCategory(t.Context(), owner, original.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMemoryCategory(createCtx, owner, "Original", "Original guidance", true); !errors.Is(err, ErrMemoryCategoryCommittedResultMissing) {
		t.Fatalf("deleted committed create was recreated or uncertain: %v", err)
	}
	if _, err := store.UpdateMemoryCategory(updateCtx, owner, original.ID, UpdateMemoryCategoryInput{Name: &name}); !errors.Is(err, ErrMemoryCategoryCommittedResultMissing) {
		t.Fatalf("deleted committed update lost proof: %v", err)
	}
	categories, err := store.ListMemoryCategories(t.Context(), owner)
	if err != nil || len(categories) != maxMemoryCategoriesPerUser-1 {
		t.Fatalf("replay recreated a deleted category: count=%d err=%v", len(categories), err)
	}
	var raw string
	if err := store.db.QueryRow(`SELECT result_json FROM mutation_result_ledger WHERE owner_user_id=? AND idempotency_key=? AND operation=?`, owner, "original-create", memoryCategoryCreateOperation).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != fmt.Sprintf(`{"categoryId":%q}`, original.ID) {
		t.Fatalf("receipt retained extra category data: %s", raw)
	}
}

func TestMemoryCategoryMutationFailuresRemainUnknownAndAtomic(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			store := categoryReceiptFixture(t)
			const owner = "rollback-owner"
			var category MemoryCategory
			var err error
			if operation == "update" {
				category, err = store.CreateMemoryCategory(categoryReceiptContext(t, "seed"), owner, "Before", "Before guidance", true)
				if err != nil {
					t.Fatal(err)
				}
			}
			const key = "rejected-sql-write"
			// Deliberately misleading SQL text must NOT acquire a business-rejected
			// classification; both the category write and receipt must roll back.
			if _, err := store.db.Exec(`CREATE TRIGGER reject_memory_category_receipt BEFORE INSERT ON mutation_result_ledger
            WHEN NEW.idempotency_key='rejected-sql-write' BEGIN SELECT RAISE(ABORT,'unique synthetic SQL rejection'); END`); err != nil {
				t.Fatal(err)
			}
			name := "After"
			ctx := categoryReceiptContext(t, key)
			save := func() (MemoryCategory, error) {
				if operation == "create" {
					return store.CreateMemoryCategory(ctx, owner, name, "After guidance", true)
				}
				return store.UpdateMemoryCategory(ctx, owner, category.ID, UpdateMemoryCategoryInput{Name: &name})
			}
			if _, err := save(); !errors.Is(err, ErrMemoryCategoryOutcomeUnknown) || errors.Is(err, ErrMemoryCategoryWriteRejected) {
				t.Fatalf("SQL failure falsely proved rejection: %v", err)
			}
			rows, err := store.ListMemoryCategories(t.Context(), owner)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "create" && len(rows) != 0 {
				t.Fatalf("unreceipted category escaped rollback: %+v", rows)
			}
			if operation == "update" && (len(rows) != 1 || rows[0].Name != "Before") {
				t.Fatalf("unreceipted update escaped rollback: %+v", rows)
			}
			var count int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE owner_user_id=? AND idempotency_key=?`, owner, key).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed transaction left receipt: count=%d err=%v", count, err)
			}
			if _, err := store.db.Exec(`DROP TRIGGER reject_memory_category_receipt`); err != nil {
				t.Fatal(err)
			}
			saved, err := save()
			if err != nil || saved.Name != name {
				t.Fatalf("explicit same-key retry failed: %+v err=%v", saved, err)
			}
			replay, err := save()
			if err != nil || replay.ID != saved.ID || !replay.UpdatedAt.Equal(saved.UpdatedAt) {
				t.Fatalf("exact retry repeated a write: %+v err=%v", replay, err)
			}
		})
	}
}

func TestMemoryCategoryReceiptReadbackRequiresExactOwnerAndLiveContext(t *testing.T) {
	store := categoryReceiptFixture(t)
	const owner = "readback-owner"
	const key = "ack-unknown"
	input := memoryCategoryCreateRequest{Name: "Readback", Guidance: "Readback guidance", AutoRecall: true}
	ctx := categoryReceiptContext(t, key)
	saved, err := store.CreateMemoryCategory(ctx, owner, input.Name, input.Guidance, input.AutoRecall)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := newMemoryCategoryMutation(ctx, owner, memoryCategoryCreateOperation, input)
	if err != nil {
		t.Fatal(err)
	}
	// Existing real receipt + the actual post-error readback core. This does
	// not claim to inject a physical SQL Commit ACK loss.
	actual, err := store.resolveMemoryCategoryMutationError(ctx, identity, errors.New("synthetic uncertain acknowledgement"))
	if err != nil || actual.ID != saved.ID || !actual.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Fatalf("exact receipt did not recover acknowledgement: %+v err=%v", actual, err)
	}
	wrong := identity
	wrong.requestHash = "different"
	if _, err := store.resolveMemoryCategoryMutationError(ctx, wrong, errors.New("synthetic failure")); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("different request hash accepted: %v", err)
	}
	wrong = identity
	wrong.owner = "other-owner"
	if _, err := store.resolveMemoryCategoryMutationError(ctx, wrong, errors.New("synthetic failure")); !errors.Is(err, ErrMemoryCategoryOutcomeUnknown) {
		t.Fatalf("absent foreign receipt falsely acknowledged: %v", err)
	}
	cancelled, cancel := context.WithCancelCause(ctx)
	cause := errors.New("synthetic caller cancellation")
	cancel(cause)
	// A cancelled caller may not detach and consult even an already stored
	// receipt. A fresh explicit request must perform that consultation.
	if _, err := store.resolveMemoryCategoryMutationError(cancelled, identity, errors.New("synthetic failure")); !errors.Is(err, ErrMemoryCategoryOutcomeUnknown) || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("cancelled readback detached or lost cause: %v", err)
	}
	if _, err := store.DeleteMemoryCategory(t.Context(), owner, saved.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.resolveMemoryCategoryMutationError(ctx, identity, errors.New("synthetic failure")); !errors.Is(err, ErrMemoryCategoryCommittedResultMissing) {
		t.Fatalf("known committed missing result lost proof: %v", err)
	}
	if _, err := store.CreateMemoryCategory(categoryReceiptContext(t, "proven-invalid"), owner, "profile", "Valid guidance", true); !errors.Is(err, ErrMemoryCategoryWriteRejected) || errors.Is(err, ErrMemoryCategoryOutcomeUnknown) {
		t.Fatalf("deterministic validation not proven: %v", err)
	}
}
