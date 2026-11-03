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

type requiredArtifactHeadFixture struct {
	store         *Store
	first, second ArtifactVersion
}

func newRequiredArtifactHeadFixture(t *testing.T) requiredArtifactHeadFixture {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	versions := make([]ArtifactVersion, 0, 2)
	for _, content := range []string{"first", "second"} {
		_, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{
			ArtifactID: "artifact", ProjectID: "project", Name: "report.txt", Kind: "text/plain", Content: []byte(content),
		})
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, version)
	}
	return requiredArtifactHeadFixture{store: store, first: versions[0], second: versions[1]}
}

func (f requiredArtifactHeadFixture) write(required bool, expected *string, content, key, owner string) (ArtifactVersion, error) {
	_, version, err := f.store.WriteArtifactVersionRealtime(WithMutationIdempotencyKey(context.Background(), key), WriteArtifactVersionInput{
		ArtifactID: "artifact", ProjectID: "project", Name: "report.txt", ContentType: "text/plain",
		Content: strings.NewReader(content), ParentVersionID: f.first.ID, ExpectedHeadVersionID: expected,
		RequireExpectedHeadForNewMutation: required, FreshUserEditMappings: true,
	}, owner)
	return version, err
}

func (f requiredArtifactHeadFixture) state(t *testing.T) map[string]int {
	t.Helper()
	state := make(map[string]int)
	for _, table := range []string{"artifacts", "artifact_versions", "artifact_version_provenance", "workspace_outbox", "mutation_result_ledger"} {
		var count int
		if err := f.store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		state[table] = count
	}
	if err := filepath.WalkDir(f.store.blobRoot, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			state["blob_files"]++
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestRequiredArtifactHeadRejectsNewMutationAfterOwnership(t *testing.T) {
	f := newRequiredArtifactHeadFixture(t)
	before := f.state(t)
	if _, err := f.write(true, nil, "stale legacy draft", "missing-head", "owner"); !errors.Is(err, ErrArtifactExpectedHeadRequired) {
		t.Errorf("missing manual head = %v, want typed precondition", err)
	}
	if after := f.state(t); !reflect.DeepEqual(before, after) {
		t.Errorf("rejected mutation changed durable state: before=%v after=%v", before, after)
	}
	if _, err := f.write(true, nil, "foreign", "foreign", "other-owner"); err == nil || errors.Is(err, ErrArtifactExpectedHeadRequired) {
		t.Fatalf("foreign owner learned precondition: %v", err)
	}
	if after := f.state(t); !reflect.DeepEqual(before, after) {
		t.Fatal("foreign mutation changed durable state")
	}
}

func TestRequiredArtifactHeadPreservesLegacyReceiptAndDefaultProducer(t *testing.T) {
	f := newRequiredArtifactHeadFixture(t)
	legacy, err := f.write(false, nil, "accepted legacy draft", "legacy-accepted", "owner")
	if err != nil {
		t.Fatal(err)
	}
	newHead, err := f.write(false, nil, "later producer", "producer", "owner")
	if err != nil {
		t.Fatal(err)
	}
	before := f.state(t)
	replayed, err := f.write(true, nil, "accepted legacy draft", "legacy-accepted", "owner")
	if err != nil || replayed.ID != legacy.ID {
		t.Fatalf("legacy receipt replay = %+v, %v", replayed, err)
	}
	_, current, _, err := f.store.GetCurrentArtifactVersionMetadata("artifact")
	if err != nil || current.ID != newHead.ID || !reflect.DeepEqual(before, f.state(t)) {
		t.Fatal("receipt replay mutated current head or durable counts")
	}
	if _, err := f.write(true, nil, "changed legacy draft", "legacy-accepted", "owner"); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("legacy changed request = %v", err)
	}
	if _, err := f.write(true, &newHead.ID, "accepted legacy draft", "legacy-accepted", "owner"); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("injected expected changed legacy request hash = %v", err)
	}
	if !reflect.DeepEqual(before, f.state(t)) {
		t.Fatal("rejected replay changed durable state")
	}
}

func TestRequiredArtifactHeadKeepsConditionalHistoricalRestore(t *testing.T) {
	f := newRequiredArtifactHeadFixture(t)
	before := f.state(t)
	_, err := f.write(true, &f.first.ID, "stale", "stale", "owner")
	var conflict *ArtifactHeadConflictError
	if !errors.As(err, &conflict) || !reflect.DeepEqual(before, f.state(t)) {
		t.Fatalf("stale conditional = %v", err)
	}
	restored, err := f.write(true, &f.second.ID, "first", "restore", "owner")
	if err != nil || restored.ParentID != f.first.ID || restored.VersionNumber != 3 {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	before = f.state(t)
	replay, err := f.write(true, &f.second.ID, "first", "restore", "owner")
	if err != nil || replay.ID != restored.ID || !reflect.DeepEqual(before, f.state(t)) {
		t.Fatalf("conditional lost ACK = %+v, %v", replay, err)
	}
}
