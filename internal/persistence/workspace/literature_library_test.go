package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestLiteratureIndependentOwnershipSchema(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN
		('literature_items','literature_attachments','literature_attachment_versions','project_literature')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("independent literature ownership tables=%d, want 4", count)
	}
	rows, err := store.db.Query(`PRAGMA foreign_key_list(literature_attachment_versions)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, seq int
		var table, from, to, update, deletion, match string
		if err := rows.Scan(&id, &seq, &table, &from, &to, &update, &deletion, &match); err != nil {
			t.Fatal(err)
		}
		if table == "projects" || table == "artifacts" || table == "artifact_versions" {
			t.Fatalf("Library version content has project-owned FK to %s", table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestLiteratureImportIndependentBytesAfterProjectDeletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	for _, id := range []string{"source", "destination"} {
		if _, err = store.CreateProject(CreateProjectInput{ID: id, UserID: "owner", Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	content := []byte("%PDF-1.4\nimmutable-original-version\n%%EOF")
	artifact, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{ArtifactID: "paper", ProjectID: "source", Name: "paper.pdf", Kind: "application/pdf", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	input := LiteratureImport{OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID, Title: "Independent paper", ProjectID: "source", PageCount: 2}
	first, err := store.ImportLiteratureArtifact(ctx, "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.SaveArtifactVersion(SaveArtifactVersionInput{ArtifactID: "paper", ProjectID: "source", Name: "paper.pdf", Kind: "application/pdf", Content: []byte("%PDF-1.4\nnew-current-head\n%%EOF")}); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteProject("source"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.ImportLiteratureArtifact(ctx, "owner", input)
	if err != nil || replayed.VersionID != first.VersionID {
		t.Fatalf("deleted-source replay=%#v err=%v", replayed, err)
	}
	page, err := store.ListLiteratureItems(ctx, "owner", "", "", "", 20)
	if err != nil || len(page.Items) != 1 || len(page.Items[0].ProjectIDs) != 0 {
		t.Fatalf("Library after source deletion=%#v err=%v", page, err)
	}
	_, independent, reader, err := store.OpenLiteratureVersionContent(ctx, "owner", first.VersionID, false)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(actual, content) || independent.Checksum != version.ContentSHA256 {
		t.Fatalf("independent version content changed, err=%v", err)
	}
	if _, _, _, err = store.OpenLiteratureVersionContent(ctx, "other", first.VersionID, true); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign owner read=%v", err)
	}
	input.Title = "changed retry"
	if _, err = store.ImportLiteratureArtifact(ctx, "owner", input); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed operation replay=%v", err)
	}
	removed := true
	if _, err = store.PatchLiteratureItem(ctx, "owner", first.Item.ID, LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &removed}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = store.OpenLiteratureVersionContent(ctx, "owner", first.VersionID, false); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("new tombstone read=%v", err)
	}
	_, _, reader, err = store.OpenLiteratureVersionContent(ctx, "owner", first.VersionID, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
}

func TestLiteratureBlobCommitRecoversIndependentVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	artifact, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{ArtifactID: "paper", ProjectID: "project", Name: "p.pdf", Kind: "application/pdf", Content: []byte("%PDF-1.4\nfixture\n%%EOF")})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ImportLiteratureArtifact(ctx, "owner", LiteratureImport{OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID, Title: "Paper", PageCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	v := receipt.Item.Attachments[0].Versions[0]
	final, _ := store.blobAbsolute(v.StoragePath)
	stageRelative := "staging/.artifact-write-library-crash"
	staging, _ := store.blobAbsolute(stageRelative)
	if err = os.Rename(final, staging); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO blob_commit_markers(id,kind,aggregate_id,staging_path,final_path,sha256,size_bytes,created_at) VALUES(?,?,?,?,?,?,?,?)`, uuid.NewString(), "literature-attachment-version", v.ID, stageRelative, v.StoragePath, v.Checksum, v.SizeBytes, store.now().UTC()); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, _, reader, err := store.OpenLiteratureVersionContent(ctx, "owner", v.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if _, err = os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging was not finalized: %v", err)
	}
}

func libraryMutationFixture(t *testing.T) (*Store, LiteratureImport, LiteratureReceipt) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err = store.CreateProject(CreateProjectInput{ID: "source", UserID: "owner", Name: "Source"}); err != nil {
		t.Fatal(err)
	}
	artifact, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{ArtifactID: "paper", ProjectID: "source", Name: "paper.pdf", Kind: "application/pdf", Content: []byte("%PDF-1.4\nimmutable PDF fixture\n%%EOF")})
	if err != nil {
		t.Fatal(err)
	}
	input := LiteratureImport{OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID, Title: "Original", PageCount: 2}
	receipt, err := store.ImportLiteratureArtifact(context.Background(), "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	return store, input, receipt
}

func TestLiteratureMutationReceiptsRemainFrozenAcrossLaterChanges(t *testing.T) {
	store, _, imported := libraryMutationFixture(t)
	ctx := context.Background()
	firstTitle, secondTitle := "First edit", "Later edit"
	first := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Title: &firstTitle}
	receipt, err := store.PatchLiteratureItem(ctx, "owner", imported.Item.ID, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PatchLiteratureItem(ctx, "owner", imported.Item.ID, LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 2, Title: &secondTitle}); err != nil {
		t.Fatal(err)
	}
	replay, err := store.PatchLiteratureItem(ctx, "owner", imported.Item.ID, first)
	if err != nil || replay.Item.Title != receipt.Item.Title || replay.Item.MetadataRevision != 2 || len(replay.Item.Attachments) != 1 {
		t.Fatalf("patch original ACK changed: %#v %v", replay, err)
	}
	operation := uuid.NewString()
	linked, err := store.SetLiteratureProject(ctx, "owner", imported.Item.ID, "source", operation, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetLiteratureProject(ctx, "owner", imported.Item.ID, "source", uuid.NewString(), false); err != nil {
		t.Fatal(err)
	}
	linkedReplay, err := store.SetLiteratureProject(ctx, "owner", imported.Item.ID, "source", operation, true)
	if err != nil || len(linked.Item.ProjectIDs) != 1 || len(linkedReplay.Item.ProjectIDs) != 1 {
		t.Fatalf("membership original ACK changed: %#v %v", linkedReplay, err)
	}
	live, err := store.GetLiteratureItem(ctx, "owner", imported.Item.ID)
	if err != nil || live.Title != secondTitle || len(live.ProjectIDs) != 0 {
		t.Fatalf("ACK replay repeated effect: %#v %v", live, err)
	}
}

func TestLiteratureImportReplayDoesNotClaimUnavailableBytes(t *testing.T) {
	store, input, receipt := libraryMutationFixture(t)
	path, err := store.blobAbsolute(receipt.Item.Attachments[0].Versions[0].StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ImportLiteratureArtifact(context.Background(), "owner", input); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("missing bytes accepted from ledger: %v", err)
	}
	var count int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM literature_items`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retry created replacement count=%d err=%v", count, err)
	}
}

func TestLiteratureMutationReceiptReadsWholeTransactionSnapshot(t *testing.T) {
	t.Run("patch captures membership at its commit point", func(t *testing.T) {
		store, _, imported := libraryMutationFixture(t)
		ctx := context.Background()
		// A deterministic database-side change at the mutation boundary stands
		// in for a membership write after the old out-of-transaction read.
		if _, err := store.db.Exec(`CREATE TRIGGER literature_test_patch_membership AFTER UPDATE OF title ON literature_items
		 BEGIN INSERT INTO project_literature(project_id,item_id,added_at) VALUES('source',NEW.id,NEW.updated_at); END`); err != nil {
			t.Fatal(err)
		}
		title := "At commit"
		input := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Title: &title}
		receipt, err := store.PatchLiteratureItem(ctx, "owner", imported.Item.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		live, err := store.GetLiteratureItem(ctx, "owner", imported.Item.ID)
		if err != nil || !reflect.DeepEqual(receipt.Item, live) {
			t.Fatalf("patch receipt is not its complete commit snapshot: receipt=%#v live=%#v err=%v", receipt.Item, live, err)
		}
		if _, err = store.db.Exec(`DROP TRIGGER literature_test_patch_membership`); err != nil {
			t.Fatal(err)
		}
		if _, err = store.SetLiteratureProject(ctx, "owner", imported.Item.ID, "source", uuid.NewString(), false); err != nil {
			t.Fatal(err)
		}
		replay, err := store.PatchLiteratureItem(ctx, "owner", imported.Item.ID, input)
		replayedWire, _ := json.Marshal(replay)
		originalWire, _ := json.Marshal(receipt)
		if err != nil || !bytes.Equal(replayedWire, originalWire) {
			t.Fatalf("patch replay changed its original transaction snapshot: %#v %v", replay, err)
		}
	})
	t.Run("membership captures metadata at its commit point", func(t *testing.T) {
		store, _, imported := libraryMutationFixture(t)
		ctx := context.Background()
		if _, err := store.db.Exec(`CREATE TRIGGER literature_test_membership_metadata AFTER INSERT ON project_literature
		 BEGIN UPDATE literature_items SET title='Committed metadata',metadata_revision=metadata_revision+1,
		 metadata_json='{"authors":"Committed author"}' WHERE id=NEW.item_id; END`); err != nil {
			t.Fatal(err)
		}
		operation := uuid.NewString()
		receipt, err := store.SetLiteratureProject(ctx, "owner", imported.Item.ID, "source", operation, true)
		if err != nil {
			t.Fatal(err)
		}
		live, err := store.GetLiteratureItem(ctx, "owner", imported.Item.ID)
		if err != nil || !reflect.DeepEqual(receipt.Item, live) {
			t.Fatalf("membership receipt is not its complete commit snapshot: receipt=%#v live=%#v err=%v", receipt.Item, live, err)
		}
		if _, err = store.db.Exec(`DROP TRIGGER literature_test_membership_metadata`); err != nil {
			t.Fatal(err)
		}
		later := "After commit"
		if _, err = store.PatchLiteratureItem(ctx, "owner", imported.Item.ID, LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: live.MetadataRevision, Title: &later}); err != nil {
			t.Fatal(err)
		}
		replay, err := store.SetLiteratureProject(ctx, "owner", imported.Item.ID, "source", operation, true)
		replayedWire, _ := json.Marshal(replay)
		originalWire, _ := json.Marshal(receipt)
		if err != nil || !bytes.Equal(replayedWire, originalWire) {
			t.Fatalf("membership replay changed its original transaction snapshot: %#v %v", replay, err)
		}
	})
}
