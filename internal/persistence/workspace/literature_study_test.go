package workspace

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func studyTestItem(t *testing.T, s *Store) (LiteratureStudySource, LiteratureStudyGenerate) {
	t.Helper()
	created, err := s.CreateManualLiterature(t.Context(), "study-owner", LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Synthetic evidence", Metadata: map[string]any{"abstract": "The synthetic study reports an association."}})
	if err != nil {
		t.Fatal(err)
	}
	source, view, err := s.ReadLiteratureStudy(t.Context(), "study-owner", created.Item.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	return source, LiteratureStudyGenerate{OperationID: uuid.NewString(), Language: "en-US", ExpectedSourceKey: source.Key, ExpectedRevision: view.Revision}
}
func studyTestSnapshot(source LiteratureStudySource) LiteratureStudySnapshot {
	question := "A synthetic research question"
	return LiteratureStudySnapshot{SourceKey: source.Key, Coverage: "abstract", Model: "synthetic-model", Sections: LiteratureStudySections{Question: &question}}
}
func TestLiteratureStudyReadyReplayAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "study.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	source, input := studyTestItem(t, store)
	first, err := store.PublishLiteratureStudy(t.Context(), "study-owner", source.ItemID, input, studyTestSnapshot(source))
	if err != nil || first.Revision != 1 || first.Snapshot == nil {
		t.Fatalf("publish: %+v %v", first, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, read, err := store.ReadLiteratureStudy(t.Context(), "study-owner", source.ItemID, "en-US")
	if err != nil || !reflect.DeepEqual(first, read) {
		t.Fatalf("durable read: %+v %v", read, err)
	}
	replay, found, err := store.ReplayLiteratureStudy(t.Context(), "study-owner", source.ItemID, input)
	if err != nil || !found || !reflect.DeepEqual(first, replay) {
		t.Fatal(replay, found, err)
	}
	changed := input
	changed.Language = "zh-CN"
	if _, _, err := store.ReplayLiteratureStudy(t.Context(), "study-owner", source.ItemID, changed); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("same-ID different args: %v", err)
	}
	_, other, err := store.ReadLiteratureStudy(t.Context(), "study-owner", source.ItemID, "zh-CN")
	if err != nil || other.Snapshot != nil || other.Revision != 0 {
		t.Fatal("language isolation", other, err)
	}
}
func TestLiteratureStudyOwnerSourceAndPublicationFences(t *testing.T) {
	store := candidateTestStore(t)
	source, input := studyTestItem(t, store)
	ready := studyTestSnapshot(source)
	if _, _, err := store.ReadLiteratureStudy(t.Context(), "foreign-owner", source.ItemID, "en-US"); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatal("owner access", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.PublishLiteratureStudy(cancelled, "study-owner", source.ItemID, input, ready); err == nil {
		t.Fatal("cancel published")
	}
	if _, err := store.PublishLiteratureStudy(t.Context(), "study-owner", source.ItemID, input, ready); err != nil {
		t.Fatal(err)
	}
	other := input
	other.OperationID = uuid.NewString()
	if _, err := store.PublishLiteratureStudy(t.Context(), "study-owner", source.ItemID, other, ready); !errors.Is(err, ErrLiteratureStudyPublicationConflict) {
		t.Fatal("late publication overwrote winner", err)
	}
	title := "Source edited while inference was running"
	if _, err := store.PatchLiteratureItem(t.Context(), "study-owner", source.ItemID, LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Title: &title}); err != nil {
		t.Fatal(err)
	}
	other.ExpectedRevision = 1
	if _, err := store.PublishLiteratureStudy(t.Context(), "study-owner", source.ItemID, other, ready); !errors.Is(err, ErrLiteratureStudySourceChanged) {
		t.Fatal("late source published", err)
	}
	_, current, err := store.ReadLiteratureStudy(t.Context(), "study-owner", source.ItemID, "en-US")
	if err != nil || current.Snapshot != nil || current.Revision != 1 {
		t.Fatal("old source exposed", current, err)
	}
	if _, _, err := store.ReplayLiteratureStudy(t.Context(), "study-owner", source.ItemID, input); !errors.Is(err, ErrLiteratureStudySourceChanged) {
		t.Fatal("old receipt bypassed source", err)
	}
	deleted := true
	if _, err := store.PatchLiteratureItem(t.Context(), "study-owner", source.ItemID, LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 2, Deleted: &deleted}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReadLiteratureStudy(t.Context(), "study-owner", source.ItemID, "en-US"); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatal("deleted item exposed", err)
	}
}
func TestLiteratureStudySectionsAndSourceSelection(t *testing.T) {
	valid := `{"question":null,"population":null,"methods":null,"results":null,"limitations":null}`
	if _, err := DecodeLiteratureStudySections([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, strings.Replace(valid, `"question":null`, `"question":null,"question":null`, 1), strings.Replace(valid, `"question":null`, `"question":""`, 1), strings.Replace(valid, `"methods":null`, `"methods":4`, 1), strings.Replace(valid, `"question":null`, `"extra":null`, 1), valid + ` {}`, strings.Replace(valid, `"question":null`, `"question":"`+strings.Repeat("x", 2401)+`"`, 1)} {
		if _, err := DecodeLiteratureStudySections([]byte(raw)); err == nil {
			t.Fatalf("invalid result accepted: %s", raw[:min(len(raw), 80)])
		}
	}
	item := LiteratureItem{ID: "item", MetadataRevision: 3, Attachments: []LiteratureAttachment{{ID: "z", Kind: "fullText", Versions: []LiteratureVersion{{ID: "last", MIMEType: "application/pdf", VersionNumber: 1}}}, {ID: "a", Kind: "fullText", Versions: []LiteratureVersion{{ID: "older", VersionNumber: 1, MIMEType: "application/pdf"}, {ID: "newer", AttachmentID: "a", VersionNumber: 2, MIMEType: "application/pdf", Checksum: strings.Repeat("a", 64), SizeBytes: 10}}}}}
	source := literatureStudySource(item)
	if source.Version == nil || source.Version.ID != "newer" {
		t.Fatal("wrong immutable selection", source)
	}
	item.Attachments[1].Versions[1].Checksum = strings.Repeat("b", 64)
	if literatureStudySource(item).Key == source.Key {
		t.Fatal("replacement failed to invalidate source")
	}
}
func TestLiteratureStudyMigration91RollbackAndIdempotence(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 90); err != nil {
		t.Fatal(err)
	}
	before := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if _, err := db.Exec(`CREATE TRIGGER fail_study_marker BEFORE INSERT ON workspace_schema_migrations WHEN NEW.version=91 BEGIN SELECT RAISE(ABORT,'injected study marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 91); err == nil || !strings.Contains(err.Error(), "injected study marker failure") {
		t.Fatal("missing rollback failure", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='literature_study_snapshots'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial DDL", count, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_study_marker`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 91); err != nil {
		t.Fatal(err)
	}
	after := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if len(after) != 91 || !reflect.DeepEqual(before, after[:90]) {
		t.Fatal("old migration identities changed")
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 91); err != nil {
		t.Fatal(err)
	}
	if got := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery); !reflect.DeepEqual(got, after) {
		t.Fatal("migration replay changed journal")
	}
}
