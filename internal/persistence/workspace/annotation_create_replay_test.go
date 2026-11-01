package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func annotationReplayFixture(t *testing.T) (*Store, string, CreateAnnotationInput) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "annotations.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "note-project", UserID: "note-owner", Name: "Synthetic notes"}); err != nil {
		t.Fatal(err)
	}
	_, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{ArtifactID: "note-source", ProjectID: "note-project", Name: "notes.txt", Kind: "text/plain", Content: []byte("Synthetic annotation source")})
	if err != nil {
		t.Fatal(err)
	}
	return store, path, CreateAnnotationInput{ProjectID: "note-project", TargetKind: "artifact", TargetKey: "av:" + version.ID, ContentChecksum: version.ContentSHA256,
		Body: map[string]any{"type": "point", "text": "Original note 🧪", "x_percent": float64(0), "y_percent": float64(0)}}
}

func annotationReplayCounts(t *testing.T, store *Store, notes, receipts int) {
	t.Helper()
	var actualNotes, actualReceipts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM annotations`).Scan(&actualNotes); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE operation='annotation.create'`).Scan(&actualReceipts); err != nil {
		t.Fatal(err)
	}
	if actualNotes != notes || actualReceipts != receipts {
		t.Fatalf("notes/receipts=%d/%d want%d/%d", actualNotes, actualReceipts, notes, receipts)
	}
}

func TestAnnotationCreateReplayPersistsAfterLostResponseAndReopen(t *testing.T) {
	store, path, input := annotationReplayFixture(t)
	first, err := store.CreateAnnotationOwned(WithMutationIdempotencyKey(t.Context(), "lost-response"), input, "note-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	retry, err := reopened.CreateAnnotationOwned(WithMutationIdempotencyKey(t.Context(), "lost-response"), input, "note-owner")
	if err != nil || retry.ID != first.ID || retry.LabelIndex != first.LabelIndex || !retry.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("durable replay changed original note identity: %v", err)
	}
	annotationReplayCounts(t, reopened, 1, 1)
}

func TestAnnotationCreateReplayHashesCompleteNormalizedInputAndNamespacesOwner(t *testing.T) {
	store, _, input := annotationReplayFixture(t)
	key := WithMutationIdempotencyKey(t.Context(), "complete-request")
	input.Body["large_json_integer"] = json.Number("9007199254740993")
	first, err := store.CreateAnnotationOwned(key, input, "note-owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*CreateAnnotationInput){
		func(i *CreateAnnotationInput) { i.ProjectID = "another-project" },
		func(i *CreateAnnotationInput) { i.TargetKind = "local" },
		func(i *CreateAnnotationInput) { i.TargetKey = "av:another-version" },
		func(i *CreateAnnotationInput) { i.ContentChecksum = "changed-checksum" },
		func(i *CreateAnnotationInput) { i.Body["text"] = "Changed note" },
		func(i *CreateAnnotationInput) { i.Body["selection_prefix"] = "Changed anchor" },
		func(i *CreateAnnotationInput) { i.Body["large_json_integer"] = json.Number("9007199254740992") },
	} {
		changed := input
		changed.Body = cloneAnnotationBody(input.Body)
		change(&changed)
		if _, err := store.CreateAnnotationOwned(key, changed, "note-owner"); !errors.Is(err, ErrMutationIdempotencyConflict) {
			t.Fatalf("changed input did not conflict: %v", err)
		}
	}
	padded := input
	padded.ProjectID, padded.TargetKind, padded.TargetKey = " "+input.ProjectID+" ", " artifact ", " "+input.TargetKey+" "
	padded.ContentChecksum = " " + input.ContentChecksum + " "
	if replay, err := store.CreateAnnotationOwned(key, padded, " note-owner "); err != nil || replay.ID != first.ID {
		t.Fatalf("normalized replay: %v", err)
	}
	if _, err := store.CreateAnnotationOwned(key, input, "foreign-owner"); !errors.Is(err, ErrAnnotationReadSource) {
		t.Fatalf("wrong owner replay: %v", err)
	}
	if _, err := store.CreateProject(CreateProjectInput{ID: "foreign-project", UserID: "foreign-owner", Name: "Separate owner"}); err != nil {
		t.Fatal(err)
	}
	_, ownVersion, err := store.SaveArtifactVersion(SaveArtifactVersionInput{ArtifactID: "foreign-source", ProjectID: "foreign-project", Name: "own.txt", Kind: "text/plain", Content: []byte("Own source")})
	if err != nil {
		t.Fatal(err)
	}
	own := input
	own.ProjectID, own.TargetKey, own.ContentChecksum = "foreign-project", "av:"+ownVersion.ID, ownVersion.ContentSHA256
	other, err := store.CreateAnnotationOwned(key, own, "foreign-owner")
	if err != nil || other.ID == first.ID {
		t.Fatalf("actor/key namespaces were merged: %v", err)
	}
	annotationReplayCounts(t, store, 2, 2)
}

func TestAnnotationCreateReplayConcurrentSameKeyDoesNotDuplicate(t *testing.T) {
	store, path, input := annotationReplayFixture(t)
	second, err := OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	type outcome struct {
		note Annotation
		err  error
	}
	results := make(chan outcome, 8)
	start := make(chan struct{})
	var tasks sync.WaitGroup
	for index := 0; index < 8; index++ {
		current := store
		if index%2 != 0 {
			current = second
		}
		tasks.Add(1)
		go func(current *Store) {
			defer tasks.Done()
			<-start
			note, err := current.CreateAnnotationOwned(WithMutationIdempotencyKey(t.Context(), "concurrent-note"), input, "note-owner")
			results <- outcome{note, err}
		}(current)
	}
	close(start)
	tasks.Wait()
	close(results)
	var id string
	successes := 0
	for result := range results {
		if result.err != nil {
			// A bounded busy/commit uncertainty is resumable, not permission
			// to execute an unowned replacement operation with a new key.
			if !errors.Is(result.err, ErrAnnotationCreateOutcomeUnknown) {
				t.Errorf("concurrent create: %v", result.err)
			}
			continue
		}
		successes++
		if id != "" && id != result.note.ID {
			t.Fatal("same actor/key created different notes")
		}
		id = result.note.ID
	}
	if successes == 0 {
		t.Fatal("no concurrent operation committed")
	}
	replay, err := store.CreateAnnotationOwned(WithMutationIdempotencyKey(t.Context(), "concurrent-note"), input, "note-owner")
	if err != nil || replay.ID != id {
		t.Fatalf("same-key continuation failed: %v", err)
	}
	annotationReplayCounts(t, store, 1, 1)
}

func TestAnnotationCreateReplayReturnsCurrentEditAndNeverResurrectsDeletedNote(t *testing.T) {
	store, _, input := annotationReplayFixture(t)
	ctx := WithMutationIdempotencyKey(t.Context(), "edit-delete-note")
	original, err := store.CreateAnnotationOwned(ctx, input, "note-owner")
	if err != nil {
		t.Fatal(err)
	}
	edited, err := store.UpdateAnnotationTextOwned(t.Context(), original.ID, "note-owner", "User edited this note")
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.CreateAnnotationOwned(ctx, input, "note-owner")
	if err != nil || current.ID != original.ID || current.Body["text"] != edited.Body["text"] || current.UpdatedAt == nil {
		t.Fatalf("replay overwrote edit: %v", err)
	}
	if deleted, err := store.DeleteAnnotationOwned(t.Context(), original.ID, "note-owner"); err != nil || !deleted {
		t.Fatalf("delete: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := store.CreateAnnotationOwned(ctx, input, "note-owner"); !errors.Is(err, ErrAnnotationCommittedResultMissing) {
			t.Fatalf("deleted replay: %v", err)
		}
		annotationReplayCounts(t, store, 0, 1)
	}
}

func TestAnnotationCreateReplayRechecksCurrentOwnerAndPreservesLegacyNoKey(t *testing.T) {
	store, _, input := annotationReplayFixture(t)
	ctx := WithMutationIdempotencyKey(t.Context(), "current-owner")
	original, err := store.CreateAnnotationOwned(ctx, input, "note-owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE projects SET user_id='new-owner' WHERE id='note-project'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAnnotationOwned(ctx, input, "note-owner"); !errors.Is(err, ErrAnnotationReadSource) {
		t.Fatalf("revoked owner replay: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE projects SET user_id='note-owner' WHERE id='note-project'`); err != nil {
		t.Fatal(err)
	}
	restored, err := store.CreateAnnotationOwned(ctx, input, "note-owner")
	if err != nil || restored.ID != original.ID {
		t.Fatalf("restored owner lost receipt: %v", err)
	}
	first, err := store.CreateAnnotationOwned(t.Context(), input, "note-owner")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateAnnotationOwned(t.Context(), input, "note-owner")
	if err != nil || first.ID == second.ID {
		t.Fatalf("missing key deduplicated independent legacy calls: %v", err)
	}
	if _, err := store.CreateAnnotationOwned(WithMutationIdempotencyKey(t.Context(), "bad key"), input, "note-owner"); !errors.Is(err, ErrInvalidMutationIdempotencyKey) {
		t.Fatalf("explicit invalid key was ignored: %v", err)
	}
	annotationReplayCounts(t, store, 3, 3)
}

func TestAnnotationCreateReplayReceiptAndNoteRollbackAtomically(t *testing.T) {
	store, _, input := annotationReplayFixture(t)
	_, err := store.db.Exec(`CREATE TRIGGER fail_note_receipt BEFORE INSERT ON mutation_result_ledger
        WHEN NEW.operation='annotation.create' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationIdempotencyKey(t.Context(), "atomic-note")
	if _, err := store.CreateAnnotationOwned(ctx, input, "note-owner"); !errors.Is(err, ErrAnnotationCreateOutcomeUnknown) {
		t.Fatalf("failed receipt write was called a confirmed outcome: %v", err)
	}
	annotationReplayCounts(t, store, 0, 0)
	if _, err := store.db.Exec(`DROP TRIGGER fail_note_receipt`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAnnotationOwned(ctx, input, "note-owner"); err != nil {
		t.Fatal(err)
	}
	annotationReplayCounts(t, store, 1, 1)
}

func TestAnnotationCreateReplayUsesOriginalKeyForFiniteReadbackAndCancellation(t *testing.T) {
	store, _, input := annotationReplayFixture(t)
	ctx := WithMutationIdempotencyKey(t.Context(), "unknown-note")
	original, err := store.CreateAnnotationOwned(ctx, input, "note-owner")
	if err != nil {
		t.Fatal(err)
	}
	identity, frozen, err := newAnnotationCreateMutation(ctx, input, "note-owner")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.resolveAnnotationCreateError(ctx, identity, frozen, errors.New("synthetic uncertain response"))
	if err != nil || replay.ID != original.ID {
		t.Fatalf("exact readback: %v", err)
	}
	cancelled, cancel := context.WithCancelCause(ctx)
	cause := errors.New("synthetic request ended")
	cancel(cause)
	if _, err := store.CreateAnnotationOwned(cancelled, input, "note-owner"); !errors.Is(err, ErrAnnotationCreateOutcomeUnknown) || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("cancelled request lost unknown outcome: %v", err)
	}
	replay, err = store.CreateAnnotationOwned(ctx, input, "note-owner")
	if err != nil || replay.ID != original.ID {
		t.Fatalf("cancel continuation: %v", err)
	}
	annotationReplayCounts(t, store, 1, 1)
}
