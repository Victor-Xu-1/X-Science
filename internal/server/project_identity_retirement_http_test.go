package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func projectIdentityHTTP(t *testing.T, client *http.Client, base, method, path, owner string, input any) (int, []byte) {
	t.Helper()
	var body io.Reader
	if input != nil {
		body = bytes.NewReader(projectPathTestJSON(input))
	}
	request, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	// Match the existing loopback test account boundary; requests still traverse
	// a real TCP listener and the production handler, SQLite and filesystem.
	request.Host = "localhost"
	request.Header.Set("X-Science-User-Id", owner)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, workspaceRequestLimit))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, raw
}

func TestProjectIdentityHTTPRejectsReuseAfterSuccessfulDeletion(t *testing.T) {
	app, handler, _ := newProjectPathAuthTestApp(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	const id = "http-retired"
	status, raw := projectIdentityHTTP(t, client, server.URL, http.MethodPost, "/api/go/projects", "alice", map[string]any{"id": id, "name": "Original"})
	if status != http.StatusOK {
		t.Fatalf("create status=%d body=%s", status, raw)
	}
	managed, err := app.defaultAgentKernelWorkspace(id)
	if err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(managed, "evidence.txt")
	if err := os.WriteFile(evidence, []byte("alice evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, raw = projectIdentityHTTP(t, client, server.URL, http.MethodDelete, "/api/go/projects/"+id, "alice", nil)
	if status != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", status, raw)
	}
	for _, owner := range []string{"alice", "bob"} {
		status, raw = projectIdentityHTTP(t, client, server.URL, http.MethodPost, "/api/go/projects", owner, map[string]any{"id": id, "name": "Replacement"})
		if status != http.StatusBadRequest || !bytes.Contains(raw, []byte("project identity has already been used")) {
			t.Fatalf("reuse owner=%s status=%d body=%s", owner, status, raw)
		}
	}
	if _, found, err := app.workspaceStore.GetProject(id); err != nil || found {
		t.Fatalf("retired project reappeared: found=%v err=%v", found, err)
	}
	if raw, err := os.ReadFile(evidence); err != nil || string(raw) != "alice evidence" {
		t.Fatalf("retained data changed: %q err=%v", raw, err)
	}
	status, raw = projectIdentityHTTP(t, client, server.URL, http.MethodPost, "/api/go/projects", "bob", map[string]any{"id": "fresh-http-identity", "name": "New project"})
	if status != http.StatusOK {
		t.Fatalf("fresh identity status=%d body=%s", status, raw)
	}
}

func TestProjectIdentityHTTPRejectsUnknownLegacyDirectoryOnCreate(t *testing.T) {
	app, handler, _ := newProjectPathAuthTestApp(t)
	const id = "legacy-retired"
	managed, err := app.defaultAgentKernelWorkspace(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(managed, 0o700); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(managed, "legacy-evidence.txt")
	if err := os.WriteFile(evidence, []byte("keep legacy evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	status, raw := projectIdentityHTTP(t, client, server.URL, http.MethodPost, "/api/go/projects", "bob", map[string]any{"id": id, "name": "Replacement"})
	if status < http.StatusBadRequest {
		t.Fatalf("legacy directory inherited: status=%d body=%s", status, raw)
	}
	if _, found, err := app.workspaceStore.GetProject(id); err != nil || found {
		t.Fatalf("failed legacy create published project: found=%v err=%v", found, err)
	}
	if raw, err := os.ReadFile(evidence); err != nil || string(raw) != "keep legacy evidence" {
		t.Fatalf("legacy evidence disturbed: %q err=%v", raw, err)
	}
}
