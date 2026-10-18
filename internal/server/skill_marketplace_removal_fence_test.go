package server

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMarketplaceRemovalRevalidatesReplacementAfterPublicationLock(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "marketplace-removal-replacement"
	setKernelSkillMarketplaceInstallTransport(t, fixture, name, nil)
	compatJSONRequest(t, fixture.server.Handler(), http.MethodPost, "/api/marketplace/import",
		fixture.bound.access.UserID, map[string]any{
			"repo": "acme/skills", "sha": marketplaceTestSHA, "skills": []string{name},
		}, http.StatusCreated)
	replacement := prepareSkillPublicationTest(t, fixture.server, name, "New independently published user bytes.", true)
	destination := filepath.Join(fixture.server.fileRoot, "skills", name)
	fixture.server.skillMutationMu.Lock()
	locked := true
	defer func() {
		if locked {
			fixture.server.skillMutationMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() {
		done <- fixture.server.removeWebSkillMarketplaceSource(fixture.bound.access.UserID, "acme-skills")
	}()
	waitForMarketplaceRemovalMutex(t)
	// This is the original publication primitive while owning the same mutex,
	// exactly the competing installation the removal has been waiting behind.
	if err := installSkillDirectoryAtomic(replacement.sourceRoot, destination, true); err != nil {
		t.Fatal(err)
	}
	current := replacement.skill
	current.Path = filepath.Join(destination, replacement.relativeManifest)
	fixture.server.skillCatalog.UpsertSkill(current)
	fixture.server.skillMutationMu.Unlock()
	locked = false
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "changed after import") {
			t.Fatalf("removal used a pre-lock hash and accepted replacement: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("marketplace removal did not finish after publication unlocked")
	}
	raw, err := os.ReadFile(filepath.Join(destination, "SKILL.md"))
	if err != nil || !bytes.Contains(raw, []byte("New independently published user bytes.")) {
		t.Fatalf("removal deleted the replacement: %q err=%v", raw, err)
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); !found {
		t.Fatal("removal deleted replacement catalog entry")
	}
	sources, err := fixture.server.loadWebSkillMarketplaceSources(fixture.bound.access.UserID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("rejected removal changed source provenance: %#v err=%v", sources, err)
	}
}

// The production code needs no scheduler hook. Observe a bounded real mutex
// wait so the test cannot pass merely because the competing call ran too late.
func waitForMarketplaceRemovalMutex(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	buffer := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		size := runtime.Stack(buffer, true)
		for _, stack := range strings.Split(string(buffer[:size]), "\n\n") {
			if strings.Contains(stack, "(*Server).removeWebSkillMarketplaceSource(") &&
				(strings.Contains(stack, "sync.(*Mutex).Lock(") || strings.Contains(stack, "sync.(*Mutex).lockSlow(")) {
				return
			}
		}
		runtime.Gosched()
	}
	t.Fatal("marketplace removal never reached its publication mutex wait")
}
