package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"x-science/internal/literaturediscovery"
)

const publicationStoreOwner = "publication-store-owner"
const publicationStoreDOI = "10.1234/original"

func seedPublicationStoreItem(t *testing.T, store *Store, id string) literaturediscovery.PublicationScope {
	t.Helper()
	seedLiteratureCollectionItem(t, store, publicationStoreOwner, id, "Synthetic reference")
	receipt, err := store.PatchLiteratureItem(t.Context(), publicationStoreOwner, id, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: 1,
		Metadata: map[string]any{"doi": publicationStoreDOI, "opaque": json.Number("9007199254740993")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return literaturediscovery.PublicationScope{OwnerUserID: publicationStoreOwner, ItemID: id,
		OperationID: uuid.NewString(), ExpectedMetadataRevision: receipt.Item.MetadataRevision}
}

func publicationStoreSnapshot() literaturediscovery.PublicationSnapshot {
	return literaturediscovery.PublicationSnapshot{DOI: publicationStoreDOI,
		CheckedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Notices:   []literaturediscovery.PublicationNotice{{DOI: "10.1234/correction", Type: "correction", Source: "publisher", Date: "2026-01-01"}}}
}

func publicationStoreRaw(t *testing.T, store *Store, item string) string {
	t.Helper()
	var value string
	if err := store.db.QueryRow(`SELECT snapshot_json FROM literature_publication_checks WHERE item_id=?`, item).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func publicationStoreLedgerCount(t *testing.T, store *Store, scope literaturediscovery.PublicationScope) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE owner_user_id=? AND idempotency_key=? AND operation=?`,
		scope.OwnerUserID, scope.OperationID, literaturePublicationOperation).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPublicationCheckPreservesMetadataAndReplaysAfterReopen(t *testing.T) {
	store, path := literatureCollectionStoreFixture(t)
	scope := seedPublicationStoreItem(t, store, "publication-replay")
	before, err := store.GetLiteratureItem(t.Context(), scope.OwnerUserID, scope.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PreparePublicationCheck(t.Context(), scope)
	if err != nil || prepared.DOI != publicationStoreDOI || prepared.Replay != nil {
		t.Fatal(prepared, err)
	}
	snapshot := publicationStoreSnapshot()
	first, err := store.CommitPublicationCheck(t.Context(), scope, prepared.DOI, &snapshot, "")
	if err != nil || first.Schema != literaturediscovery.PublicationReceiptSchema || first.OperationID != scope.OperationID ||
		first.Result.Issue != "" || !reflect.DeepEqual(first.Result.Snapshot, &snapshot) {
		t.Fatal(first, err)
	}
	after, err := store.GetLiteratureItem(t.Context(), scope.OwnerUserID, scope.ItemID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("source-only check changed catalogue: before=%+v after=%+v err=%v", before, after, err)
	}
	var originalACK string
	if err := store.db.QueryRow(`SELECT result_json FROM mutation_result_ledger WHERE owner_user_id=? AND idempotency_key=? AND operation=?`,
		scope.OwnerUserID, scope.OperationID, literaturePublicationOperation).Scan(&originalACK); err != nil {
		t.Fatal(err)
	}
	title := "Later independent title"
	later, err := store.PatchLiteratureItem(t.Context(), scope.OwnerUserID, scope.ItemID, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: after.MetadataRevision, Title: &title,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetPublicationCheck(t.Context(), scope.OwnerUserID, scope.ItemID, later.Item.MetadataRevision)
	if err != nil || got.MetadataRevision != later.Item.MetadataRevision || !reflect.DeepEqual(got.Snapshot, &snapshot) {
		t.Fatalf("same DOI lost saved evidence after a title edit: %+v %v", got, err)
	}
	changed, err := store.PatchLiteratureItem(t.Context(), scope.OwnerUserID, scope.ItemID, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: later.Item.MetadataRevision,
		MetadataPatch: json.RawMessage(`{"set":{"doi":"10.1234/replacement"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err = store.GetPublicationCheck(t.Context(), scope.OwnerUserID, scope.ItemID, changed.Item.MetadataRevision)
	if err != nil || got.DOI != "10.1234/replacement" || got.Snapshot != nil {
		t.Fatalf("changed DOI reused old evidence: %+v %v", got, err)
	}
	deleted := true
	if _, err := store.PatchLiteratureItem(t.Context(), scope.OwnerUserID, scope.ItemID, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: changed.Item.MetadataRevision, Deleted: &deleted,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	prepared, err = reopened.PreparePublicationCheck(t.Context(), scope)
	if err != nil || !reflect.DeepEqual(prepared.Replay, &first) {
		t.Fatalf("original ACK lost after edits/delete/reopen: %+v %v", prepared, err)
	}
	replay, err := reopened.CommitPublicationCheck(t.Context(), scope, publicationStoreDOI, &snapshot, "")
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatalf("commit replay changed original result: %+v %v", replay, err)
	}
	var replayACK string
	if err := reopened.db.QueryRow(`SELECT result_json FROM mutation_result_ledger WHERE owner_user_id=? AND idempotency_key=? AND operation=?`,
		scope.OwnerUserID, scope.OperationID, literaturePublicationOperation).Scan(&replayACK); err != nil || replayACK != originalACK {
		t.Fatal("replay rewrote original ACK bytes", err)
	}
}

func TestPublicationCheckFencesOwnerRevisionDeletionAndDOIAtCommit(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	for _, kind := range []string{"owner", "missing", "revision", "deleted", "doi"} {
		t.Run(kind, func(t *testing.T) {
			scope := seedPublicationStoreItem(t, store, "publication-cas-"+kind)
			prepared, err := store.PreparePublicationCheck(t.Context(), scope)
			if err != nil || prepared.DOI != publicationStoreDOI {
				t.Fatal(prepared, err)
			}
			want := ErrLiteratureConflict
			switch kind {
			case "owner":
				scope.OwnerUserID, want = "foreign-owner", ErrLiteratureUnavailable
			case "missing":
				scope.ItemID, want = "missing-publication-item", ErrLiteratureUnavailable
			default:
				patch := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: scope.ExpectedMetadataRevision}
				title, deleted := "Edited during provider request", true
				if kind == "revision" {
					patch.Title = &title
				}
				if kind == "deleted" {
					patch.Deleted, want = &deleted, ErrLiteratureUnavailable
				}
				if kind == "doi" {
					patch.MetadataPatch = json.RawMessage(`{"set":{"doi":"10.1234/changed"}}`)
				}
				updated, err := store.PatchLiteratureItem(t.Context(), scope.OwnerUserID, scope.ItemID, patch)
				if err != nil {
					t.Fatal(err)
				}
				// DOI changes must be rejected independently of the revision fence.
				if kind != "revision" {
					scope.ExpectedMetadataRevision = updated.Item.MetadataRevision
				}
			}
			if kind != "doi" {
				if _, err := store.GetPublicationCheck(t.Context(), scope.OwnerUserID, scope.ItemID, scope.ExpectedMetadataRevision); !errors.Is(err, want) {
					t.Fatalf("saved read after %s: %v, want %v", kind, err, want)
				}
				if _, err := store.PreparePublicationCheck(t.Context(), scope); !errors.Is(err, want) {
					t.Fatalf("prepare after %s: %v, want %v", kind, err, want)
				}
			}
			snapshot := publicationStoreSnapshot()
			if _, err := store.CommitPublicationCheck(t.Context(), scope, prepared.DOI, &snapshot, ""); !errors.Is(err, want) {
				t.Fatalf("commit after %s: %v, want %v", kind, err, want)
			}
			if count := publicationStoreLedgerCount(t, store, scope); count != 0 {
				t.Fatalf("rejected identity committed %d receipts", count)
			}
			var count int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM literature_publication_checks WHERE item_id=?`, scope.ItemID).Scan(&count); err != nil || count != 0 {
				t.Fatal("rejected identity saved evidence", count, err)
			}
		})
	}
}

func TestPublicationCheckRejectsChangedOperationAndRollsBackEvidenceWithLedger(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	scope := seedPublicationStoreItem(t, store, "publication-atomic")
	snapshot := publicationStoreSnapshot()
	if _, err := store.CommitPublicationCheck(t.Context(), scope, publicationStoreDOI, &snapshot, ""); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []literaturediscovery.PublicationScope{
		{OwnerUserID: scope.OwnerUserID, ItemID: "different-item", OperationID: scope.OperationID, ExpectedMetadataRevision: scope.ExpectedMetadataRevision},
		{OwnerUserID: scope.OwnerUserID, ItemID: scope.ItemID, OperationID: scope.OperationID, ExpectedMetadataRevision: scope.ExpectedMetadataRevision + 1},
	} {
		if _, err := store.PreparePublicationCheck(t.Context(), changed); !errors.Is(err, ErrMutationIdempotencyConflict) {
			t.Fatalf("changed request escaped prepare: %v", err)
		}
		if _, err := store.CommitPublicationCheck(t.Context(), changed, publicationStoreDOI, &snapshot, ""); !errors.Is(err, ErrMutationIdempotencyConflict) {
			t.Fatalf("changed request escaped commit: %v", err)
		}
	}
	before := publicationStoreRaw(t, store, scope.ItemID)
	next := scope
	next.OperationID = uuid.NewString()
	newer := snapshot
	newer.CheckedAt, newer.Notices = snapshot.CheckedAt.Add(time.Hour), []literaturediscovery.PublicationNotice{}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_publication_ledger BEFORE INSERT ON mutation_result_ledger
 WHEN NEW.operation='literature.publication-check' BEGIN SELECT RAISE(ABORT,'injected publication ledger failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitPublicationCheck(t.Context(), next, publicationStoreDOI, &newer, ""); err == nil || !strings.Contains(err.Error(), "injected publication ledger failure") {
		t.Fatalf("expected real ledger failure: %v", err)
	}
	if publicationStoreRaw(t, store, scope.ItemID) != before || publicationStoreLedgerCount(t, store, next) != 0 {
		t.Fatal("failed ledger write left partial evidence or receipt")
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_publication_ledger`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitPublicationCheck(t.Context(), next, publicationStoreDOI, &newer, ""); err != nil {
		t.Fatalf("rolled-back operation could not be retried: %v", err)
	}
	got, err := store.GetPublicationCheck(t.Context(), scope.OwnerUserID, scope.ItemID, scope.ExpectedMetadataRevision)
	if err != nil || !reflect.DeepEqual(got.Snapshot, &newer) {
		t.Fatal(got, err)
	}
}

func TestPublicationCheckTerminalFailureKeepsEvidenceAndCorruptSavedResultCanRepair(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	scope := seedPublicationStoreItem(t, store, "publication-failure")
	snapshot := publicationStoreSnapshot()
	if _, err := store.CommitPublicationCheck(t.Context(), scope, publicationStoreDOI, &snapshot, ""); err != nil {
		t.Fatal(err)
	}
	before := publicationStoreRaw(t, store, scope.ItemID)
	failed := scope
	failed.OperationID = uuid.NewString()
	receipt, err := store.CommitPublicationCheck(t.Context(), failed, publicationStoreDOI, nil, literaturediscovery.PublicationRateLimited)
	if err != nil || receipt.Result.Issue != literaturediscovery.PublicationRateLimited || !reflect.DeepEqual(receipt.Result.Snapshot, &snapshot) {
		t.Fatal(receipt, err)
	}
	if publicationStoreRaw(t, store, scope.ItemID) != before || publicationStoreLedgerCount(t, store, failed) != 1 {
		t.Fatal("definite failure rewrote evidence time or lost its terminal ACK")
	}
	const corrupt = `{"doi":"10.1234/original","checked_at":"2026-01-02T03:04:05Z","notices":null}`
	if _, err := store.db.Exec(`UPDATE literature_publication_checks SET snapshot_json=? WHERE item_id=?`, corrupt, scope.ItemID); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetPublicationCheck(t.Context(), scope.OwnerUserID, scope.ItemID, scope.ExpectedMetadataRevision)
	if err != nil || got.Issue != literaturediscovery.PublicationResponseInvalid || got.Snapshot != nil {
		t.Fatalf("corrupt saved data became success: %+v %v", got, err)
	}
	repair := scope
	repair.OperationID = uuid.NewString()
	newer := snapshot
	newer.CheckedAt = newer.CheckedAt.Add(time.Hour)
	if _, err := store.CommitPublicationCheck(t.Context(), repair, publicationStoreDOI, &newer, ""); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetPublicationCheck(t.Context(), scope.OwnerUserID, scope.ItemID, scope.ExpectedMetadataRevision)
	if err != nil || got.Issue != "" || !reflect.DeepEqual(got.Snapshot, &newer) {
		t.Fatalf("explicit successful check failed to repair saved data: %+v %v", got, err)
	}
	prepared, err := store.PreparePublicationCheck(t.Context(), failed)
	if err != nil || !reflect.DeepEqual(prepared.Replay, &receipt) {
		t.Fatalf("later repair rewrote original failure ACK: %+v %v", prepared, err)
	}
}

func TestPublicationCheckCancelledCommitPreservesPriorEvidenceAndOriginalSlot(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	scope := seedPublicationStoreItem(t, store, "publication-cancel")
	snapshot := publicationStoreSnapshot()
	if _, err := store.CommitPublicationCheck(t.Context(), scope, publicationStoreDOI, &snapshot, ""); err != nil {
		t.Fatal(err)
	}
	before := publicationStoreRaw(t, store, scope.ItemID)
	next := scope
	next.OperationID = uuid.NewString()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.CommitPublicationCheck(ctx, next, publicationStoreDOI, nil, literaturediscovery.PublicationTimeout); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled commit=%v", err)
	}
	if publicationStoreRaw(t, store, scope.ItemID) != before || publicationStoreLedgerCount(t, store, next) != 0 {
		t.Fatal("cancelled caller left terminal receipt or changed evidence")
	}
	prepared, err := store.PreparePublicationCheck(t.Context(), next)
	if err != nil || prepared.Replay != nil || prepared.DOI != publicationStoreDOI {
		t.Fatal("cancelled operation was falsely settled", prepared, err)
	}
}
