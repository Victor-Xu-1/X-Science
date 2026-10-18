package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillMultipartImportMetadataFailureDoesNotPublish(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "multipart-metadata-rollback"
	if err := os.Mkdir(filepath.Join(fixture.server.fileRoot, "settings.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	postSkillBundle(t, fixture.server.Handler(), "/api/skills/import", fixture.bound.access.UserID,
		name+".md", []byte(kernelSkillManifestForTest(name, "Multipart package.")), http.StatusInternalServerError)
	if _, err := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", name)); !os.IsNotExist(err) {
		t.Fatalf("failed multipart metadata left published files: %v", err)
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
		t.Fatal("failed multipart metadata left a runtime catalog entry")
	}
}

func TestKernelSkillMarketplaceInstallCannotInjectCommitAuthority(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "no-json-commit-authority"
	network := setKernelSkillMarketplaceInstallTransport(t, fixture, name, nil)
	for _, field := range []string{"commit", "execution", "guard"} {
		_, err := fixture.call("host.skills.install", map[string]any{
			"repo": "acme/skills", "sha": marketplaceTestSHA, "skills": []any{name}, field: "immediate",
		})
		if err == nil {
			t.Fatalf("kernel JSON accepted internal authority field %q", field)
		}
	}
	if network.requests != 0 {
		t.Fatalf("injected authority caused %d network requests", network.requests)
	}
	if _, err := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", name)); !os.IsNotExist(err) {
		t.Fatalf("injected authority published a Skill: %v", err)
	}
}
