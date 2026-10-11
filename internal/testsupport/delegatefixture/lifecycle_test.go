package delegatefixture

import (
	"context"
	"path/filepath"
	"testing"

	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

func TestFixtureClearRetainsIdentityAndOtherRootAcrossReseed(t *testing.T) {
	store := fixtureLifecycleStore(t)
	first, err := Seed(store)
	if err != nil {
		t.Fatal(err)
	}
	project, _, _ := store.GetProject(ProjectID)
	parent := fixtureFrame(t, store, ParentFrameID)
	other, err := store.CreateFrame(workspace.CreateFrameInput{ID: "unrelated-root", ProjectID: ProjectID, AgentName: "OPERON", ConversationType: "agent", Name: "Retained", Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	otherStream, err := repository.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "frame:" + other.ID, OwnerID: UserID, ExternalID: other.ID, SessionID: other.ID,
		ProjectID: ProjectID, RootFrameID: other.ID, FrameID: other.ID, Kind: transcriptstore.StreamKindFrameRef, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := Clear(store)
	if err != nil || !removed {
		t.Fatalf("clear: removed=%v err=%v", removed, err)
	}
	if _, found, err := store.GetFrame(ParentFrameID); err != nil || found {
		t.Fatalf("fixture parent retained: found=%v err=%v", found, err)
	}
	retained, found, err := store.GetProject(ProjectID)
	if err != nil || !found || retained.CreatedAt != project.CreatedAt || retained.Path != project.Path {
		t.Fatalf("project identity replaced: %#v found=%v err=%v", retained, found, err)
	}
	if removed, err := Clear(store); err != nil || removed {
		t.Fatalf("second clear: removed=%v err=%v", removed, err)
	}
	second, err := Seed(store)
	if err != nil || second != first {
		t.Fatalf("reseed contract changed: %#v err=%v", second, err)
	}
	if fresh := fixtureFrame(t, store, ParentFrameID); fresh.IncarnationID == parent.IncarnationID || fresh.MessageCount != 9 {
		t.Fatalf("tree was not replaced: %#v", fresh)
	}
	if retained, found, err := store.GetFrame(other.ID); err != nil || !found || retained.IncarnationID != other.IncarnationID {
		t.Fatalf("unrelated root changed: %#v found=%v err=%v", retained, found, err)
	}
	if stream, found, err := repository.GetFrameStreamBySession(t.Context(), UserID, other.ID); err != nil || !found || stream.UID != otherStream.UID {
		t.Fatalf("unrelated stream changed: %#v found=%v err=%v", stream, found, err)
	}
}

func TestFixtureClearRejectsStaleIncarnationWithoutMutation(t *testing.T) {
	store := fixtureLifecycleStore(t)
	if _, err := Seed(store); err != nil {
		t.Fatal(err)
	}
	original := fixtureFrame(t, store, ParentFrameID)
	repository, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
		return clearFixtureRootTx(context.Background(), tx, "stale-incarnation")
	}); err == nil {
		t.Fatal("stale incarnation admitted cleanup")
	}
	if retained := fixtureFrame(t, store, ParentFrameID); retained.IncarnationID != original.IncarnationID || retained.MessageCount != original.MessageCount {
		t.Fatalf("current fixture changed: %#v", retained)
	}
}

func TestFixtureClearRejectsForeignOwnerBeforeTreeMutation(t *testing.T) {
	store := fixtureLifecycleStore(t)
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: ProjectID, UserID: "other-owner", Name: "Foreign"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{ID: ParentFrameID, ProjectID: ProjectID, AgentName: "OPERON", ConversationType: "agent", Name: "Foreign", Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	original := fixtureFrame(t, store, ParentFrameID)
	if _, err := Clear(store); err == nil {
		t.Fatal("cleared another owner's fixture ID")
	}
	if _, err := Seed(store); err == nil {
		t.Fatal("replaced another owner's fixture ID")
	}
	if retained := fixtureFrame(t, store, ParentFrameID); retained.IncarnationID != original.IncarnationID {
		t.Fatal("foreign frame incarnation changed")
	}
}

func TestFixtureClearRejectsForeignProjectOrNonRootFrame(t *testing.T) {
	for _, nonRoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "project", true: "root"}[nonRoot], func(t *testing.T) {
			store := fixtureLifecycleStore(t)
			for _, id := range []string{ProjectID, "other-project"} {
				if _, err := store.CreateProject(workspace.CreateProjectInput{ID: id, UserID: UserID, Name: id}); err != nil {
					t.Fatal(err)
				}
			}
			input := workspace.CreateFrameInput{ID: ParentFrameID, ProjectID: "other-project", AgentName: "OPERON", ConversationType: "agent", Name: "Retained", Status: "completed"}
			if nonRoot {
				if _, err := store.CreateFrame(workspace.CreateFrameInput{ID: "other-root", ProjectID: ProjectID, AgentName: "OPERON", ConversationType: "agent", Name: "Retained", Status: "completed"}); err != nil {
					t.Fatal(err)
				}
				input.ProjectID, input.ParentFrameID = ProjectID, "other-root"
			}
			if _, err := store.CreateFrame(input); err != nil {
				t.Fatal(err)
			}
			original := fixtureFrame(t, store, ParentFrameID)
			if _, err := Clear(store); err == nil {
				t.Fatal("cleared a non-fixture tree")
			}
			if _, err := Seed(store); err == nil {
				t.Fatal("replaced a non-fixture tree")
			}
			if retained := fixtureFrame(t, store, ParentFrameID); retained.IncarnationID != original.IncarnationID {
				t.Fatal("unrelated frame changed")
			}
		})
	}
}

func fixtureLifecycleStore(t *testing.T) *workspace.Store {
	t.Helper()
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
