package workspace

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const readingStatusTestOwner = "reading-status-owner"

type readingStatusTestRow struct {
	Status    LiteratureReadingStatus
	UpdatedAt string
}

func readReadingStatusTestRow(t *testing.T, store *Store, id string) readingStatusTestRow {
	t.Helper()
	var row readingStatusTestRow
	if err := store.db.QueryRow(`SELECT reading_status,hex(CAST(updated_at AS BLOB)) FROM literature_items WHERE id=?`, id).Scan(&row.Status, &row.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	return row
}

func readingStatusTestLedgerCount(t *testing.T, store *Store, owner, operation string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger
 WHERE owner_user_id=? AND idempotency_key=? AND operation='literature.reading-status'`, owner, operation).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func readingStatusTestLedgerBytes(t *testing.T, store *Store, operation string) []string {
	t.Helper()
	var raw, created string
	if err := store.db.QueryRow(`SELECT result_json,hex(CAST(created_at AS BLOB)) FROM mutation_result_ledger
 WHERE owner_user_id=? AND idempotency_key=? AND operation='literature.reading-status'`, readingStatusTestOwner, operation).Scan(&raw, &created); err != nil {
		t.Fatal(err)
	}
	return []string{raw, created}
}

func TestLiteratureReadingStatusDeduplicatesAndReplaysOriginalPayloadAfterReopen(t *testing.T) {
	store, path := literatureCollectionStoreFixture(t)
	for _, id := range []string{"reading-order-a", "reading-order-b"} {
		seedLiteratureCollectionItem(t, store, readingStatusTestOwner, id, "Synthetic reference")
	}
	input := LiteratureReadingStatusInput{OperationID: uuid.NewString(), ItemIDs: []string{"reading-order-b", "reading-order-a", "reading-order-b"}, ReadingStatus: LiteratureReading}
	wantIDs := []string{"reading-order-b", "reading-order-a"}
	originalIDs := append([]string(nil), input.ItemIDs...)
	first, err := store.SetLiteratureReadingStatus(t.Context(), readingStatusTestOwner, input)
	if err != nil || first.Schema != "x-science.literature-reading-status-receipt.v1" || first.OperationID != input.OperationID ||
		first.ReadingStatus != LiteratureReading || !reflect.DeepEqual(first.ItemIDs, wantIDs) || !reflect.DeepEqual(input.ItemIDs, originalIDs) {
		t.Fatalf("wrong first-occurrence ACK or mutated caller input: %+v %v input%v", first, err, input.ItemIDs)
	}
	for _, id := range wantIDs {
		if row := readReadingStatusTestRow(t, store, id); row.Status != LiteratureReading {
			t.Fatalf("batch did not update %s: %+v", id, row)
		}
	}
	originalACK := readingStatusTestLedgerBytes(t, store, input.OperationID)
	for _, changed := range []LiteratureReadingStatusInput{
		{OperationID: input.OperationID, ItemIDs: wantIDs, ReadingStatus: LiteratureReading},
		{OperationID: input.OperationID, ItemIDs: []string{"reading-order-a", "reading-order-b", "reading-order-b"}, ReadingStatus: LiteratureReading},
		{OperationID: input.OperationID, ItemIDs: originalIDs, ReadingStatus: LiteratureRead},
	} {
		if _, err := store.SetLiteratureReadingStatus(t.Context(), readingStatusTestOwner, changed); !errors.Is(err, ErrMutationIdempotencyConflict) {
			t.Fatalf("changed original payload escaped its operation binding: %+v %v", changed, err)
		}
	}
	if _, err := store.SetLiteratureReadingStatus(t.Context(), readingStatusTestOwner,
		LiteratureReadingStatusInput{OperationID: uuid.NewString(), ItemIDs: []string{"reading-order-b"}, ReadingStatus: LiteratureRead}); err != nil {
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
	replay, err := reopened.SetLiteratureReadingStatus(t.Context(), readingStatusTestOwner, input)
	if err != nil || !reflect.DeepEqual(replay, first) || !reflect.DeepEqual(readingStatusTestLedgerBytes(t, reopened, input.OperationID), originalACK) {
		t.Fatalf("reopen/replay changed the original receipt or bytes: %+v %v", replay, err)
	}
	if row := readReadingStatusTestRow(t, reopened, "reading-order-b"); row.Status != LiteratureRead {
		t.Fatalf("historical ACK replay overwrote later reading state: %+v", row)
	}
}

func TestLiteratureReadingStatusRollsBackUnavailableTargetsAndFailedLedger(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	for _, kind := range []string{"foreign", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			owned, unavailable := "reading-owned-"+kind, "reading-unavailable-"+kind
			seedLiteratureCollectionItem(t, store, readingStatusTestOwner, owned, "Owned reference")
			owner := readingStatusTestOwner
			if kind == "foreign" {
				owner = "another-reading-owner"
			}
			seedLiteratureCollectionItem(t, store, owner, unavailable, "Unavailable reference")
			if kind == "deleted" {
				deleted := true
				if _, err := store.PatchLiteratureItem(t.Context(), owner, unavailable,
					LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted}); err != nil {
					t.Fatal(err)
				}
			}
			before := []readingStatusTestRow{readReadingStatusTestRow(t, store, owned), readReadingStatusTestRow(t, store, unavailable)}
			input := LiteratureReadingStatusInput{OperationID: uuid.NewString(), ItemIDs: []string{owned, unavailable}, ReadingStatus: LiteratureRead}
			if _, err := store.SetLiteratureReadingStatus(t.Context(), readingStatusTestOwner, input); !errors.Is(err, ErrLiteratureUnavailable) {
				t.Fatalf("mixed unavailable target was accepted: %v", err)
			}
			after := []readingStatusTestRow{readReadingStatusTestRow(t, store, owned), readReadingStatusTestRow(t, store, unavailable)}
			if !reflect.DeepEqual(after, before) || readingStatusTestLedgerCount(t, store, readingStatusTestOwner, input.OperationID) != 0 {
				t.Fatal("rejected target left partial status, timestamp or receipt")
			}
		})
	}
	ids := []string{"reading-atomic-a", "reading-atomic-b"}
	for _, id := range ids {
		seedLiteratureCollectionItem(t, store, readingStatusTestOwner, id, "Atomic reference")
	}
	before := []readingStatusTestRow{readReadingStatusTestRow(t, store, ids[0]), readReadingStatusTestRow(t, store, ids[1])}
	input := LiteratureReadingStatusInput{OperationID: uuid.NewString(), ItemIDs: ids, ReadingStatus: LiteratureRead}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_reading_status_ledger BEFORE INSERT ON mutation_result_ledger
 WHEN NEW.operation='literature.reading-status' BEGIN SELECT RAISE(ABORT,'injected reading status ledger failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLiteratureReadingStatus(t.Context(), readingStatusTestOwner, input); err == nil || !strings.Contains(err.Error(), "injected reading status ledger failure") {
		t.Fatalf("expected real ledger marker failure: %v", err)
	}
	after := []readingStatusTestRow{readReadingStatusTestRow(t, store, ids[0]), readReadingStatusTestRow(t, store, ids[1])}
	if !reflect.DeepEqual(after, before) || readingStatusTestLedgerCount(t, store, readingStatusTestOwner, input.OperationID) != 0 {
		t.Fatal("failed ledger insert did not roll back every status and timestamp")
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_reading_status_ledger`); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.SetLiteratureReadingStatus(t.Context(), readingStatusTestOwner, input)
	if err != nil || !reflect.DeepEqual(receipt.ItemIDs, ids) || receipt.OperationID != input.OperationID || readingStatusTestLedgerCount(t, store, readingStatusTestOwner, input.OperationID) != 1 {
		t.Fatalf("rolled-back original UUID could not be retried: %+v %v", receipt, err)
	}
	for _, id := range ids {
		if row := readReadingStatusTestRow(t, store, id); row.Status != LiteratureRead {
			t.Fatalf("retry did not atomically apply %s: %+v", id, row)
		}
	}
}

func TestLiteratureReadingStatusPreservesMetadataAndPublicationEvidence(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const id = "reading-independent-reference"
	seedLiteratureCollectionItem(t, store, readingStatusTestOwner, id, "Retained reference")
	if _, err := store.PatchLiteratureItem(t.Context(), readingStatusTestOwner, id, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: 1,
		Metadata: map[string]any{"doi": "10.1234/source", "opaque": json.Number("1e400"), "nested": map[string]any{"integer": json.Number("9007199254740993")}},
	}); err != nil {
		t.Fatal(err)
	}
	// This is provider evidence in the real v85 table installed by migrations,
	// not a replacement schema or a claim of a live provider request.
	const evidence = `{"doi":"10.1234/source","checked_at":"2026-01-02T03:04:05Z","notices":[{"doi":"10.1234/correction","type":"correction","source":"publisher"}]}`
	if _, err := store.db.Exec(`INSERT INTO literature_publication_checks(item_id,source_doi,snapshot_json) VALUES(?,?,?)`, id, "10.1234/source", evidence); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetLiteratureItem(t.Context(), readingStatusTestOwner, id)
	if err != nil {
		t.Fatal(err)
	}
	var metadataBefore string
	if err := store.db.QueryRow(`SELECT metadata_json FROM literature_items WHERE id=?`, id).Scan(&metadataBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLiteratureReadingStatus(t.Context(), readingStatusTestOwner,
		LiteratureReadingStatusInput{OperationID: uuid.NewString(), ItemIDs: []string{id}, ReadingStatus: LiteratureReading}); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetLiteratureItem(t.Context(), readingStatusTestOwner, id)
	if err != nil || after.MetadataRevision != before.MetadataRevision || after.Title != before.Title || !reflect.DeepEqual(after.Metadata, before.Metadata) {
		t.Fatalf("reading state rewrote bibliographic metadata: before%+v after%+v err%v", before, after, err)
	}
	nested, ok := after.Metadata["nested"].(map[string]any)
	if after.Metadata["opaque"] != json.Number("1e400") || !ok || nested["integer"] != json.Number("9007199254740993") {
		t.Fatal("status write changed opaque number precision")
	}
	var metadataAfter, sourceDOI, saved string
	if err := store.db.QueryRow(`SELECT metadata_json FROM literature_items WHERE id=?`, id).Scan(&metadataAfter); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT source_doi,snapshot_json FROM literature_publication_checks WHERE item_id=?`, id).Scan(&sourceDOI, &saved); err != nil {
		t.Fatal(err)
	}
	if metadataAfter != metadataBefore || sourceDOI != "10.1234/source" || saved != evidence {
		t.Fatal("status write changed raw metadata or prior provider evidence/timestamp")
	}
	title := "Independent metadata editor"
	edited, err := store.PatchLiteratureItem(t.Context(), readingStatusTestOwner, id,
		LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: before.MetadataRevision, Title: &title})
	if err != nil || edited.Item.MetadataRevision != before.MetadataRevision+1 || readReadingStatusTestRow(t, store, id).Status != LiteratureReading {
		t.Fatalf("status invalidated the original metadata editor revision: %+v %v", edited, err)
	}
}
