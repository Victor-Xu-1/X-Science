package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func hostFileReferenceFixture(t *testing.T, mode string) (*Server, workspace.CompatibilityFrame, string) {
	t.Helper()
	app, store := newP3WebConversationServer(t)
	project := createP3Project(t, store, "host-files-project", "local")
	frame, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "host-files-frame", ProjectID: project.ID, AgentName: "OPERON",
		Status: "completed", ConversationType: "agent", Name: "Host files",
	})
	if err != nil {
		t.Fatal(err)
	}
	compatibilityFrame, found, err := store.GetCompatibilityFrame(frame.ID)
	if err != nil || !found {
		t.Fatalf("compatibility frame found=%v err=%v", found, err)
	}
	root := t.TempDir()
	if _, err := app.upsertHostGrant("local", root, mode); err != nil {
		t.Fatal(err)
	}
	return app, compatibilityFrame, root
}

func hostFileReferenceURI(grant, relative string) string {
	return (&url.URL{Scheme: "x-science-host-grant", Host: "file", RawQuery: url.Values{
		"grant": {grant}, "path": {relative},
	}.Encode()}).String()
}

func hostFileTaskRoot(t *testing.T, app *Server, frame workspace.CompatibilityFrame) string {
	t.Helper()
	access, found, err := app.workspaceStore.GetKernelFrameAccessContext(context.Background(), frame.ID)
	if err != nil || !found {
		t.Fatalf("frame access found=%v err=%v", found, err)
	}
	root, err := app.defaultAgentKernelTaskWorkspace(access.Frame.ProjectID, access.Frame.RootFrameID, access.RootFrameIncarnationID)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestWebConversationHostGrantFileReferenceCreatesOwnedSnapshot(t *testing.T) {
	for _, mode := range []string{"read", "read_write"} {
		t.Run(mode, func(t *testing.T) {
			app, frame, root := hostFileReferenceFixture(t, mode)
			name := "sample + 测量%20.csv"
			if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "nested", name)
			want := []byte("sample,value\nA,1.25\n")
			if err := os.WriteFile(source, want, 0o600); err != nil {
				t.Fatal(err)
			}
			uri := hostFileReferenceURI(root, "nested/"+name)
			response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+frame.ID+"/messages", map[string]any{
				"content": "Inspect this snapshot.\n[[X_SCIENCE_FILES]]\n" + uri,
				"files":   []string{uri}, "loading_id": "owned-host-snapshot",
			}, "local")
			if response.Code != http.StatusAccepted {
				t.Fatalf("send status=%d body=%s", response.Code, response.Body.String())
			}
			session, found, err := app.sessionStore.Get(frame.ID)
			if err != nil || !found {
				t.Fatalf("session found=%v err=%v", found, err)
			}
			input, _ := session.Orchestration["inputData"].(map[string]any)
			files, _ := input["files"].([]any)
			if len(files) != 1 || files[0] != "inputs/"+name {
				t.Fatalf("host reference was not materialized: files=%#v", files)
			}
			request, _ := input["request"].(string)
			if strings.Contains(request, uri) || strings.Contains(request, root) || !strings.Contains(request, "inputs/"+name) {
				t.Fatalf("request did not use the task snapshot: %q", request)
			}
			snapshot := filepath.Join(hostFileTaskRoot(t, app, frame), "inputs", name)
			if got, err := os.ReadFile(snapshot); err != nil || string(got) != string(want) {
				t.Fatalf("snapshot=%q err=%v", got, err)
			}
			if err := os.WriteFile(source, []byte("changed later"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(snapshot); err != nil || string(got) != string(want) {
				t.Fatalf("snapshot followed later host writes: %q err=%v", got, err)
			}
			grants, err := app.loadHostGrants("local")
			if err != nil || len(grants) != 1 || grants[0].Path != root || grants[0].Mode != mode {
				t.Fatalf("reference changed grants: %#v %v", grants, err)
			}
		})
	}
}

func TestWebConversationHostGrantFileReferenceRejectsUntrustedInput(t *testing.T) {
	app, frame, root := hostFileReferenceFixture(t, "read")
	if err := os.WriteFile(filepath.Join(root, "safe.txt"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "foreign.txt"), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	valid := hostFileReferenceURI(root, "safe.txt")
	for _, tc := range []struct{ name, uri string }{
		{"parent", hostFileReferenceURI(root, "../foreign.txt")},
		{"dot-segment", hostFileReferenceURI(root, "nested/./safe.txt")},
		{"absolute", hostFileReferenceURI(root, "/safe.txt")},
		{"backslash", hostFileReferenceURI(root, `nested\safe.txt`)},
		{"empty-segment", hostFileReferenceURI(root, "nested//safe.txt")},
		{"nul", hostFileReferenceURI(root, "safe\x00.txt")},
		{"invalid-utf8", hostFileReferenceURI(root, "safe.txt") + "%FF"},
		{"duplicate-query", valid + "&path=other.txt"},
		{"unknown-query", valid + "&owner=local"},
		{"fragment", valid + "#ignored"},
		{"wrong-authority", strings.Replace(valid, "://file?", "://other?", 1)},
		{"unowned-root", hostFileReferenceURI(outside, "foreign.txt")},
		{"symlink-escape", hostFileReferenceURI(root, "escape/foreign.txt")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := newLoopbackTestRequest(http.MethodPost, "/", nil)
			if files, err := app.materializeWebConversationInputFiles(context.Background(), request, frame, []string{tc.uri}); err == nil {
				t.Fatalf("untrusted host reference accepted through loopback: %#v", files)
			}
		})
	}
	if removed, err := app.revokeHostGrant("local", root); err != nil || !removed {
		t.Fatalf("revoke=%v err=%v", removed, err)
	}
	request := newLoopbackTestRequest(http.MethodPost, "/", nil)
	if files, err := app.materializeWebConversationInputFiles(context.Background(), request, frame, []string{valid}); err == nil {
		t.Fatalf("revoked reference used legacy local-read fallback: %#v", files)
	}
}

func TestWebConversationHostGrantFileReferenceKeepsBudgetAndCancellation(t *testing.T) {
	app, frame, root := hostFileReferenceFixture(t, "read")
	oversized, err := os.Create(filepath.Join(root, "oversized.bin"))
	if err != nil {
		t.Fatal(err)
	}
	err = errors.Join(oversized.Truncate(maxWebConversationInputFileBytes+1), oversized.Close())
	if err != nil {
		t.Fatal(err)
	}
	request := newLoopbackTestRequest(http.MethodPost, "/", nil)
	if files, err := app.materializeWebConversationInputFiles(context.Background(), request, frame, []string{hostFileReferenceURI(root, "oversized.bin")}); err == nil {
		t.Fatalf("oversized reference bypassed file budget: %#v", files)
	}
	cause := errors.New("fixture cancelled before copying")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if _, err := app.materializeWebConversationInputFiles(ctx, request, frame, []string{hostFileReferenceURI(root, "oversized.bin")}); !errors.Is(err, cause) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
}
