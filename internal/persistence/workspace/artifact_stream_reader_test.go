package workspace

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
)

func TestArtifactDeleteWithOpenReaderPreservesUnreadContent(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(CreateProjectInput{ID: "project-1", UserID: "user-1", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("reader survives artifact deletion\n"), 4096)
	artifact, _, err := store.SaveArtifactVersionFromReader(context.Background(), SaveArtifactVersionReaderInput{
		ArtifactID: "artifact-1", ProjectID: "project-1", Name: "stream.txt", Kind: "text/plain", Content: bytes.NewReader(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, reader, found, err := store.OpenCurrentArtifactContent(artifact.ID)
	if err != nil || !found {
		t.Fatalf("open reader: found=%v err=%v", found, err)
	}
	defer reader.Close()
	prefix := make([]byte, 7)
	if _, err := io.ReadFull(reader, prefix); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteArtifact(artifact.ID); err != nil {
		t.Fatal(err)
	}
	remainder, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(prefix, remainder...), payload) {
		t.Fatal("reader lost unread content after deletion")
	}
	if _, found, err := store.GetArtifact(artifact.ID); err != nil || found {
		t.Fatalf("deleted metadata: found=%v err=%v", found, err)
	}
	if _, _, next, found, err := store.OpenCurrentArtifactContent(artifact.ID); err != nil || found || next != nil {
		if next != nil {
			_ = next.Close()
		}
		t.Fatalf("deleted artifact reopened: found=%v err=%v", found, err)
	}
}
