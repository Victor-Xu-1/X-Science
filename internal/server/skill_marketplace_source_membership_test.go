package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarketplaceSourceIncrementalImportsRetainEveryInstalledMember(t *testing.T) {
	verifyMarketplaceSourceMembership(t, marketplaceTestSHA, false)
}

func TestMarketplaceSourceMixedRevisionsDoNotRelabelRetainedBytes(t *testing.T) {
	verifyMarketplaceSourceMembership(t, strings.Repeat("a", 40), false)
}

func TestMarketplaceSourceLicenseEvidenceUsesThePinnedRevision(t *testing.T) {
	verifyMarketplaceSourceMembership(t, marketplaceTestSHA, true)
}

func verifyMarketplaceSourceMembership(t *testing.T, secondRevision string, requirePinnedLicense bool) {
	t.Helper()
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const first, second = "source-member-first", "source-member-second"
	archives := make(map[string][]byte)
	for _, revision := range []string{marketplaceTestSHA, secondRevision} {
		archives[revision] = marketplaceSkillArchive(t, map[string]string{
			"skills-main/skills/first/SKILL.md":  kernelSkillManifestForTest(first, "First member from "+revision),
			"skills-main/skills/second/SKILL.md": kernelSkillManifestForTest(second, "Second member from "+revision),
		})
	}
	fixture.server.httpClient = &http.Client{Transport: marketplaceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		contentType := "application/json"
		var body []byte
		switch {
		case request.URL.Host == "api.github.com" && strings.Contains(request.URL.Path, "/commits/"):
			revision := filepath.Base(request.URL.Path)
			if _, found := archives[revision]; !found {
				return nil, fmt.Errorf("unexpected commit lookup %q", revision)
			}
			body = []byte(`{"sha":"` + revision + `"}`)
		case request.URL.Host == "api.github.com" && strings.HasSuffix(request.URL.Path, "/license"):
			body = []byte(`{"license":{"spdx_id":"Apache-2.0"}}`)
			if requirePinnedLicense && request.URL.Query().Get("ref") != marketplaceTestSHA {
				// HEAD has a different license from the selected immutable pin.
				body = []byte(`{"license":{"spdx_id":"MIT"}}`)
			}
		case request.URL.Host == "codeload.github.com":
			var found bool
			body, found = archives[filepath.Base(request.URL.Path)]
			if !found {
				return nil, fmt.Errorf("unexpected archive lookup %q", request.URL.Path)
			}
			contentType = "application/zip"
		default:
			return nil, fmt.Errorf("unexpected source-membership request %s", request.URL.Redacted())
		}
		return &http.Response{StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK),
			Header: http.Header{"Content-Type": []string{contentType}},
			Body:   io.NopCloser(bytes.NewReader(body)), Request: request}, nil
	})}
	app, owner := fixture.server.Handler(), fixture.bound.access.UserID
	for index, revision := range []string{marketplaceTestSHA, secondRevision} {
		name := []string{first, second}[index]
		compatJSONRequest(t, app, http.MethodPost, "/api/marketplace/import", owner, map[string]any{
			"repo": "acme/skills", "sha": revision, "skills": []string{name},
		}, http.StatusCreated)
	}
	response := compatJSONRequest(t, app, http.MethodGet, "/api/marketplace/sources", owner, nil, http.StatusOK)
	rows, _ := response["sources"].([]any)
	if len(rows) != 1 {
		t.Fatalf("source record count=%d, want one repository", len(rows))
	}
	source := mapValue(rows[0])
	if !webSkillTestStringArrayContains(source["skills"], first) || !webSkillTestStringArrayContains(source["skills"], second) {
		t.Errorf("incremental import lost installed source membership: %#v", source["skills"])
	}
	for name, expectedRevision := range map[string]string{first: marketplaceTestSHA, second: secondRevision} {
		raw, err := os.ReadFile(filepath.Join(fixture.server.fileRoot, "skills", name, "SKILL.md"))
		if err != nil || !bytes.Contains(raw, []byte(expectedRevision)) {
			t.Errorf("installed %s bytes do not retain their real revision: %q, %v", name, raw, err)
		}
		if _, found := findCatalogSkill(fixture.server.skillCatalog, name); !found {
			t.Errorf("installed member %s disappeared from the real catalog", name)
		}
		if secondRevision != marketplaceTestSHA {
			provenance := mapValue(mapValue(source["skill_revisions"])[name])
			if provenance["sha"] != expectedRevision {
				t.Errorf("retained member %s mislabeled: provenance=%#v want=%s", name, provenance, expectedRevision)
			}
		}
	}
	if secondRevision != marketplaceTestSHA && source["sha"] != "" {
		t.Errorf("mixed installed revisions were labeled as one commit: %v", source["sha"])
	}
	if requirePinnedLicense && source["license"] != "Apache-2.0" {
		t.Errorf("pinned source claimed the mutable HEAD license: %v", source["license"])
	}
	removed := httptest.NewRecorder()
	app.ServeHTTP(removed, compatRequest(t, http.MethodDelete, "/api/marketplace/sources/acme-skills", owner, nil))
	if removed.Code != http.StatusNoContent {
		t.Fatalf("source removal status=%d body=%s", removed.Code, removed.Body.String())
	}
	for _, name := range []string{first, second} {
		if _, err := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", name)); !os.IsNotExist(err) {
			t.Errorf("successful source removal orphaned %s: %v", name, err)
		}
		if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
			t.Errorf("removed source member %s remained executable in the catalog", name)
		}
	}
}
