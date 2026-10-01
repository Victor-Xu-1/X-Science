package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Normal artifact operations and temporary SQLite back the production preview
// components. No live user state, provider, or browser profile is used.
func TestArtifactHashLinkBrowser(t *testing.T) {
	if os.Getenv("SYNON_ARTIFACT_LINK_BROWSER") != "1" {
		t.Skip("enable controlled browser integration")
	}
	f := newAgentSaveArtifactsFixture(t)
	old := saveReplyBranchArtifact(t, f, "old-version", "linked-report.md")
	if err := f.store.PublishArtifactVersion(context.Background(), webString(old["version_id"]), webString(old["artifact_id"]), f.stream.ProjectID, f.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
	latest := saveReplyBranchArtifact(t, f, "new-version", "linked-report.md")
	if err := f.store.PublishArtifactVersion(context.Background(), webString(latest["version_id"]), webString(latest["artifact_id"]), f.stream.ProjectID, f.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Synon-User-Id", f.stream.OwnerID)
		f.server.Handler().ServeHTTP(w, r)
	}))
	defer server.Close()
	frontend, err := filepath.Abs("../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"conversation": f.stream.FrameID, "old": old, "latest": latest})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", "tests/web-e2e/artifactLinks.browser.mjs")
	command.Dir = frontend
	command.Env = append(os.Environ(), "SYNON_ARTIFACT_LINK_API="+server.URL, "SYNON_ARTIFACT_LINK_FIXTURE="+string(data))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, output)
	}
	fmt.Println(string(output))
}
