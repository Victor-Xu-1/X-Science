package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebConversationInputMappingDuplicateSourcesKeepEveryReference(t *testing.T) {
	app, frame, root := hostFileReferenceFixture(t, "read")
	for _, name := range []string{"first.txt", "second.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first := hostFileReferenceURI(root, "first.txt")
	second := hostFileReferenceURI(root, "second.txt")
	response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+frame.ID+"/messages", map[string]any{
		"content": "Inspect attachments.\n[[X_SCIENCE_FILES]]\n" + strings.Join([]string{first, first, second}, "\n"),
		"files":   []string{first, first, second}, "loading_id": "duplicate-input-mapping",
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
	if len(files) != 2 || files[0] != "inputs/first.txt" || files[1] != "inputs/second.txt" {
		t.Fatalf("task-local files were not unique: %#v", files)
	}
	request, _ := input["request"].(string)
	if strings.Contains(request, "x-science-host-grant:") ||
		strings.Count(request, "inputs/first.txt") != 2 || strings.Count(request, "inputs/second.txt") != 1 {
		t.Fatalf("duplicate source shifted subsequent snapshot mappings: %q", request)
	}
}

func TestWebConversationInputMappingOverlappingSourcePaths(t *testing.T) {
	got := rewriteWebConversationAttachedFilePaths("/a /abc", []string{"/a", "/abc"}, []string{"inputs/a-digest", "inputs/abc"})
	if got != "inputs/a-digest inputs/abc" {
		t.Fatalf("shorter source consumed another selected source: %q", got)
	}
}

func TestWebConversationInputMappingDoesNotRewriteGeneratedTargets(t *testing.T) {
	got := rewriteWebConversationAttachedFilePaths("/a /b", []string{"/a", "/b"}, []string{"/b", "inputs/b"})
	if got != "/b inputs/b" {
		t.Fatalf("replacement output was processed a second time: %q", got)
	}
}

func TestWebConversationInputMappingUnchangedLongerSourceShieldsPrefix(t *testing.T) {
	got := rewriteWebConversationAttachedFilePaths("/a /abc", []string{"/a", "/abc"}, []string{"inputs/a", "/abc"})
	if got != "inputs/a /abc" {
		t.Fatalf("unchanged selected path lost its identity mapping: %q", got)
	}
}
