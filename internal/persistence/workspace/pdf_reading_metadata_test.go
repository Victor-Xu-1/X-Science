package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func readingMetadataTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "project", UserID: "local", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(CreateFrameInput{ID: "frame", ProjectID: "project", AgentName: "OPERON", Status: "completed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestPDFReadingMetadataUnsetContextSupportsExistingSubmissionLifecycle(t *testing.T) {
	for _, state := range []string{"no_row", "submission_only", "json_null", "empty_object"} {
		for _, action := range []string{"read", "write"} {
			t.Run(state+"/"+action, func(t *testing.T) {
				store := readingMetadataTestStore(t)
				if state == "submission_only" {
					if err := store.SetFrameSubmissionMetadata("frame", map[string]any{"request": "existing submission"}, false); err != nil {
						t.Fatal(err)
					}
					var unset bool
					if err := store.db.QueryRow(`SELECT context_data IS NULL FROM frame_runtime_metadata WHERE frame_id='frame'`).Scan(&unset); err != nil || !unset {
						t.Fatalf("submission-only context: unset=%t err=%v", unset, err)
					}
				} else if state != "no_row" {
					raw := "{}"
					if state == "json_null" {
						raw = "null"
					}
					if _, err := store.db.Exec(`INSERT INTO frame_runtime_metadata(frame_id,context_data) VALUES('frame',?)`, raw); err != nil {
						t.Fatal(err)
					}
				}
				if action == "write" {
					stored, err := store.SetFrameRuntimeMetadata("frame", FrameRuntimeMetadata{ContextData: map[string]any{"_model": "existing-model"}})
					if err != nil || stored.ContextData["_model"] != "existing-model" {
						t.Fatalf("runtime write: stored=%#v err=%v", stored, err)
					}
					if state == "submission_only" && stored.InputData["request"] != "existing submission" {
						t.Fatalf("submission was not preserved: %#v", stored.InputData)
					}
				}
				reading, err := store.GetPDFReadingContext(context.Background(), "local", "frame")
				if err != nil || reading.Schema != transcriptstore.PDFReadingContextSchema || reading.Revision != 0 || len(reading.Bindings) != 0 {
					t.Fatalf("empty reading context: %#v err=%v", reading, err)
				}
			})
		}
	}
}

func TestPDFReadingMetadataMalformedDocumentIsNotUnset(t *testing.T) {
	for _, raw := range []string{"[]", `"not an object"`} {
		t.Run(raw, func(t *testing.T) {
			store := readingMetadataTestStore(t)
			if _, err := store.db.Exec(`INSERT INTO frame_runtime_metadata(frame_id,context_data) VALUES('frame',?)`, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetPDFReadingContext(context.Background(), "local", "frame"); !errors.Is(err, transcriptstore.ErrPDFReadingState) {
				t.Fatalf("malformed read: %v", err)
			}
			if _, err := store.SetFrameRuntimeMetadata("frame", FrameRuntimeMetadata{ContextData: map[string]any{"_model": "replacement"}}); !errors.Is(err, transcriptstore.ErrPDFReadingState) {
				t.Fatalf("malformed write: %v", err)
			}
			var actual string
			if err := store.db.QueryRow(`SELECT context_data FROM frame_runtime_metadata WHERE frame_id='frame'`).Scan(&actual); err != nil || actual != raw {
				t.Fatalf("malformed state was overwritten: %q err=%v", actual, err)
			}
		})
	}
}
