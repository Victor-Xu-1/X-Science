package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConditionalArtifactEditKeepsLineageAndIdempotentPublication(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	_, first, err := store.SaveArtifactVersion(SaveArtifactVersionInput{ArtifactID: "artifact", ProjectID: "project", Name: "report.md", Kind: "text/markdown", Content: []byte("first")})
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := store.SaveArtifactVersion(SaveArtifactVersionInput{ArtifactID: "artifact", ProjectID: "project", Name: "report.md", Kind: "text/markdown", Content: []byte("second")})
	if err != nil {
		t.Fatal(err)
	}
	state := func() map[string]int {
		t.Helper()
		result := map[string]int{}
		for _, table := range []string{"artifact_versions", "artifact_version_provenance", "workspace_outbox", "mutation_result_ledger"} {
			// These are fixed fixture table names, never request data.
			var count int
			if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
				t.Fatal(err)
			}
			result[table] = count
		}
		if err := filepath.WalkDir(store.blobRoot, func(_ string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				result["blob_files"]++
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return result
	}
	write := func(ctx context.Context, expected *string, content, key, owner string) (ArtifactVersion, error) {
		_, version, err := store.WriteArtifactVersionRealtime(WithMutationIdempotencyKey(ctx, key), WriteArtifactVersionInput{
			ArtifactID: "artifact", ProjectID: "project", Name: "report.md", ContentType: "text/markdown",
			Content: strings.NewReader(content), ParentVersionID: first.ID, ExpectedHeadVersionID: expected, FreshUserEditMappings: true,
		}, owner)
		return version, err
	}
	before := state()
	_, err = write(context.Background(), &first.ID, "stale", "stale-edit", "owner")
	var conflict *ArtifactHeadConflictError
	if !errors.As(err, &conflict) || conflict.ExpectedHeadVersionID != first.ID || conflict.CurrentHeadVersionID != second.ID {
		t.Fatalf("stale baseline error=%v", err)
	}
	if after := state(); !reflect.DeepEqual(before, after) {
		t.Fatalf("conflict mutated durable state: before=%v after=%v", before, after)
	}
	_, err = write(context.Background(), &first.ID, "foreign", "foreign-edit", "another-owner")
	if err == nil || errors.As(err, &conflict) {
		t.Fatalf("foreign owner learned a head conflict: %v", err)
	}
	if after := state(); !reflect.DeepEqual(before, after) {
		t.Fatal("foreign edit changed durable state")
	}

	// Restoring v1 uses v1 as parent and the separately confirmed v2 head.
	third, err := write(context.Background(), &second.ID, "first", "restore-edit", "owner")
	if err != nil || third.VersionNumber != 3 || third.ParentID != first.ID {
		t.Fatalf("historical restore=%+v err=%v", third, err)
	}
	accepted := state()
	replay, err := write(context.Background(), &second.ID, "first", "restore-edit", "owner")
	if err != nil || replay.ID != third.ID {
		t.Fatalf("lost ACK replay=%+v err=%v", replay, err)
	}
	if after := state(); !reflect.DeepEqual(accepted, after) {
		t.Fatal("accepted operation replay duplicated publication")
	}
	_, err = write(context.Background(), &third.ID, "first", "restore-edit", "owner")
	if !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed head reused operation: %v", err)
	}
	_, err = write(context.Background(), &second.ID, "changed", "restore-edit", "owner")
	if !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed body reused operation: %v", err)
	}
	if after := state(); !reflect.DeepEqual(accepted, after) {
		t.Fatal("changed replay mutated state")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := write(cancelled, &third.ID, "cancelled", "cancelled-edit", "owner"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled edit=%v", err)
	}
	if after := state(); !reflect.DeepEqual(accepted, after) {
		t.Fatal("cancelled edit mutated state")
	}
}
