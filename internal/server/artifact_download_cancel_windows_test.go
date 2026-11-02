package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	workspace "x-science/internal/persistence/workspace"
)

func TestArtifactDownloadCancellationClosesReaderBeforeDeleteAndRecovery(t *testing.T) {
	database := filepath.Join(t.TempDir(), "workspace.db")
	store, err := workspace.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("cancellable artifact content\n"), 8192)
	artifact, _, err := store.SaveArtifactVersionFromReader(t.Context(), workspace.SaveArtifactVersionReaderInput{
		ArtifactID: "cancelled-download", ProjectID: "project", Name: "download.bin",
		Kind: "application/octet-stream", Content: bytes.NewReader(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SaveArtifactVersionFromReader(t.Context(), workspace.SaveArtifactVersionReaderInput{
		ArtifactID: "kept", ProjectID: "project", Name: "kept.txt", Kind: "text/plain", Content: bytes.NewBufferString("still referenced"),
	}); err != nil {
		t.Fatal(err)
	}
	_, _, content, found, err := store.OpenCurrentArtifactContent(artifact.ID)
	if err != nil || !found {
		t.Fatalf("locate canonical blob: found=%v err=%v", found, err)
	}
	file, ok := content.(*os.File)
	if !ok {
		_ = content.Close()
		t.Fatalf("expected a real file-backed artifact, got %T", content)
	}
	blob := file.Name()
	if err := content.Close(); err != nil {
		t.Fatal(err)
	}

	started, returned := make(chan struct{}), make(chan struct{})
	app := New(Options{Workspace: store}).Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		app.ServeHTTP(&cancelledArtifactResponseWriter{ResponseWriter: w, ctx: r.Context(), started: started}, r)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/artifacts/"+artifact.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Science-User-Id", "owner")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Artifact-Id") != artifact.ID {
		t.Fatalf("original download handler response: status=%d headers=%v", response.StatusCode, response.Header)
	}
	prefix := make([]byte, 16)
	if _, err := io.ReadFull(response.Body, prefix); err != nil || !bytes.Equal(prefix, payload[:len(prefix)]) {
		t.Fatalf("real HTTP prefix=%q err=%v", prefix, err)
	}
	awaitArtifactDownloadSignal(t, started, "first response body write")
	if err := artifactDownloadExclusiveOpen(blob); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("canonical reader must be open before cancellation: %v", err)
	}
	cancel()
	if _, err := io.ReadAll(response.Body); !errors.Is(err, context.Canceled) {
		t.Fatalf("client body read after cancellation: %v", err)
	}
	awaitArtifactDownloadSignal(t, returned, "original handler completion after cancellation")
	if err := artifactDownloadExclusiveOpen(blob); err != nil {
		t.Fatalf("original handler leaked its reader after cancellation: %v", err)
	}
	if _, found, err := store.GetArtifact(artifact.ID); err != nil || !found {
		t.Fatalf("cancelling a read removed artifact metadata: found=%v err=%v", found, err)
	}
	if err := store.DeleteArtifact(artifact.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(blob); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted artifact left a physical blob: %v", err)
	}
	server.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, found, err := reopened.GetArtifact(artifact.ID); err != nil || found {
		t.Fatalf("recovery restored deleted metadata: found=%v err=%v", found, err)
	}
	if _, _, reader, found, err := reopened.OpenCurrentArtifactContent(artifact.ID); err != nil || found || reader != nil {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("recovery reopened deleted content: found=%v err=%v", found, err)
	}
	_, version, found, err := reopened.GetCurrentArtifactVersion("kept")
	if err != nil || !found || string(version.Content) != "still referenced" {
		t.Fatalf("recovery lost unrelated referenced content: found=%v err=%v content=%q", found, err, version.Content)
	}
}

// Apply deterministic socket backpressure without replacing the handler, store,
// reader, transport or request context. The real client cancellation releases it.
type cancelledArtifactResponseWriter struct {
	http.ResponseWriter
	ctx     context.Context
	started chan struct{}
	once    sync.Once
}

func (w *cancelledArtifactResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if err != nil {
		return n, err
	}
	w.ResponseWriter.(http.Flusher).Flush()
	w.once.Do(func() { close(w.started) })
	<-w.ctx.Done()
	return n, w.ctx.Err()
}

func artifactDownloadExclusiveOpen(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	return windows.CloseHandle(handle)
}

func awaitArtifactDownloadSignal(t *testing.T, signal <-chan struct{}, phase string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out awaiting %s", phase)
	}
}
