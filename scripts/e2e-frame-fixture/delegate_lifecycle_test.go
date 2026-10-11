package main

import (
	"path/filepath"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
	delegatefixture "synon-go/internal/testsupport/delegatefixture"
)

func TestSeedDelegateFixtureKeepsProjectAcrossClearAndPayloadReseed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	primary, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	first, err := seedDelegateFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := workspace.OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	project, found, err := secondary.GetProject(delegatefixture.ProjectID)
	if err != nil || !found {
		t.Fatalf("project: found=%v err=%v", found, err)
	}
	if removed, err := delegatefixture.Clear(secondary); err != nil || !removed {
		t.Fatalf("clear: removed=%v err=%v", removed, err)
	}
	if err := secondary.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := seedDelegateFixture(path)
	if err != nil || second != first {
		t.Fatalf("payload reseed contract: %#v err=%v", second, err)
	}
	secondary, err = workspace.OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondary.Close()
	retained, found, err := secondary.GetProject(delegatefixture.ProjectID)
	if err != nil || !found || retained.CreatedAt != project.CreatedAt {
		t.Fatalf("fixture project replaced: %#v found=%v err=%v", retained, found, err)
	}
	status, err := secondary.SchemaStatus(t.Context())
	expected, expectedErr := workspace.ExpectedSchemaStatus()
	if err != nil || expectedErr != nil || status.CurrentVersion != expected.TargetVersion {
		t.Fatalf("secondary schema changed: %#v err=%v", status, err)
	}
}
