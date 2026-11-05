package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestConversationReadMetadataDoesNotWaitForWriter(t *testing.T) {
	store := newConversationReadPoolFixture(t)
	for _, read := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"project", func(context.Context) error {
			project, found, err := store.GetCompatibilityProject("owner", "project")
			if err == nil && (!found || project.ID != "project") {
				return errors.New("owned project was not returned")
			}
			return err
		}},
		{"other_owner", func(context.Context) error {
			_, found, err := store.GetCompatibilityProject("other", "project")
			if found {
				return errors.New("project crossed its owner fence")
			}
			return err
		}},
		{"trace", func(ctx context.Context) error {
			snapshot, err := store.GetFrameTraceSnapshotWithContext(ctx, "frame", nil)
			if err == nil && (snapshot.Root.ID != "frame" || snapshot.FrameCount != 2 || len(snapshot.Frames) != 2) {
				return errors.New("trace lost its atomic root/child snapshot")
			}
			return err
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			writer, err := store.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			before := store.db.Stats().WaitCount
			done := make(chan error, 1)
			go func() { done <- read.run(t.Context()) }()
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			for {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
					return
				case <-deadline.C:
					t.Error("read did not settle with only the unrelated writer connection held")
					goto release
				default:
				}
				if store.db.Stats().WaitCount > before {
					t.Error("pure conversation read queued on the held writer pool")
					goto release
				}
				runtime.Gosched()
			}
		release:
			_ = writer.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("read after cleanup: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Error("read did not settle after cleanup")
			}
		})
	}
}

func newConversationReadPoolFixture(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(CreateFrameInput{ID: "frame", ProjectID: "project", AgentName: "test", Status: "completed", ConversationType: "task"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(CreateFrameInput{ID: "child", ProjectID: "project", ParentFrameID: "frame", AgentName: "child", Status: "completed", ConversationType: "task"}); err != nil {
		t.Fatal(err)
	}
	return store
}
