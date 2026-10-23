package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
)

func publishSkillRevisionForTest(fixture *kernelSkillDraftFixture, name, revision string, overwrite bool) (any, error) {
	fixture.sequence++
	return fixture.server.handleKernelHostCall(fixture.bound, nil)(context.Background(), kernelruntime.HostCall{
		ID: fmt.Sprintf("hc-%032x", fixture.sequence), CellID: "skill-authoring-cell", Method: "host.skills.publish",
		Args: []any{name, overwrite}, Kwargs: map[string]any{"expected_draft_revision": revision},
	})
}

func TestSkillPublishRequiresReviewedRevision(t *testing.T) {
	for _, transport := range []string{"host", "http"} {
		t.Run(transport, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name = "reviewed-revision"
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Reviewed draft.")); err != nil {
				t.Fatal(err)
			}
			if transport == "host" {
				if result, err := fixture.call("host.skills.publish", name, false); err == nil {
					t.Fatalf("publish without reviewed identity succeeded: %#v", result)
				} else {
					requireSkillRevisionFailureForTest(t, err, "draft_revision_required")
				}
			} else {
				compatJSONRequest(t, fixture.server.Handler(), http.MethodPost, "/api/skills/"+name+"/publish", fixture.identity.access.UserID,
					map[string]any{"overwrite": false}, http.StatusPreconditionRequired)
				invalid := compatJSONRequest(t, fixture.server.Handler(), http.MethodPost, "/api/skills/"+name+"/publish", fixture.identity.access.UserID,
					map[string]any{"overwrite": false, "expected_draft_revision": "invalid"}, http.StatusPreconditionRequired)
				if invalid["code"] != "draft_revision_required" {
					t.Fatalf("untyped required response: %#v", invalid)
				}
			}
			if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
				t.Fatal("missing revision published the draft")
			}
			if _, found, err := fixture.server.findCompatibilitySkillDraft(name); err != nil || !found {
				t.Fatalf("draft lost: %v", err)
			}
		})
	}
}

func TestSkillPublishRejectsChangedResourceRevision(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "reviewed-resource"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Read references/guide.md.")); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Reviewed helper."); err != nil {
		t.Fatal(err)
	}
	read, err := fixture.call("host.skills.read", name)
	if err != nil {
		t.Fatal(err)
	}
	revision := stringValue(mapValue(read)["draft_revision"])
	// Old servers do not return a revision and ignore this precondition entirely.
	// Retain a well-formed value so the pre-fix failure proves publication, not compilation.
	if revision == "" {
		revision = strings.Repeat("0", 64)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Unreviewed helper.", "Reviewed helper."); err != nil {
		t.Fatal(err)
	}
	if result, err := publishSkillRevisionForTest(fixture, name, revision, false); err == nil {
		t.Fatalf("stale reviewed package was published: %#v", result)
	} else {
		requireSkillRevisionFailureForTest(t, err, "draft_revision_conflict")
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
		t.Fatal("stale package entered runtime catalog")
	}
	draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
	if err != nil || !found {
		t.Fatalf("conflict removed draft: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(draft.Path), "references", "guide.md"))
	if err != nil || string(content) != "Unreviewed helper." {
		t.Fatalf("conflict changed draft bytes: %q %v", content, err)
	}
}

func requireSkillRevisionFailureForTest(t *testing.T, err error, code string) {
	t.Helper()
	var failure *kernelruntime.HostCallError
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("failure=%v, want %s", err, code)
	}
}

func reviewedSkillRevisionForTest(t *testing.T, fixture *kernelSkillDraftFixture, name string) string {
	t.Helper()
	read, err := fixture.call("host.skills.read", name)
	if err != nil {
		t.Fatal(err)
	}
	revision := stringValue(mapValue(read)["draft_revision"])
	if !skillDraftRevisionPattern.MatchString(revision) {
		t.Fatalf("invalid read revision: %#v", read)
	}
	validated, err := fixture.call("host.skills.validate", name)
	if err != nil || mapValue(validated)["valid"] != true || mapValue(validated)["draft_revision"] != revision {
		t.Fatalf("validation snapshot mismatch: %#v %v", validated, err)
	}
	return revision
}

func TestSkillPublishRevisionStableReadbackAndOverwrite(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "stable-reviewed-package"
	manifest := kernelSkillManifestForTest(name, "Reviewed bytes.")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "assets/data.txt", "Stable resource."); err != nil {
		t.Fatal(err)
	}
	revision := reviewedSkillRevisionForTest(t, fixture, name)
	draft, _, _ := fixture.server.findCompatibilitySkillDraft(name)
	stamp := time.Unix(123456789, 0)
	if err := os.Chtimes(draft.Path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if again := reviewedSkillRevisionForTest(t, fixture, name); again != revision {
		t.Fatal("mtime changed content identity")
	}
	if _, err := publishSkillRevisionForTest(fixture, name, "invalid", false); err == nil {
		t.Fatal("invalid token published")
	} else {
		requireSkillRevisionFailureForTest(t, err, "draft_revision_required")
	}
	if _, err := fixture.call("host.skills.publish", name, false, revision); err != nil {
		t.Fatal(err)
	}
	published, err := fixture.call("host.skills.read", "local:"+name)
	if err != nil || mapValue(published)["content"] != manifest || mapValue(published)["draft_revision"] != nil {
		t.Fatalf("published read=%#v %v", published, err)
	}
	if _, found, err := fixture.server.findCompatibilitySkillDraft(name); err != nil || found {
		t.Fatalf("published draft retained unexpectedly: %v", err)
	}
	if _, err := fixture.call("host.skills.edit", name, "assets/data.txt", "Reviewed replacement.", "Stable resource."); err != nil {
		t.Fatal(err)
	}
	replacement := reviewedSkillRevisionForTest(t, fixture, name)
	if _, err := publishSkillRevisionForTest(fixture, name, replacement, false); err == nil {
		t.Fatal("revision bypassed explicit overwrite")
	}
	if _, err := publishSkillRevisionForTest(fixture, name, replacement, true); err != nil {
		t.Fatal(err)
	}
	read, err := fixture.call("host.skills.read", "local:"+name, "assets/data.txt")
	if err != nil || mapValue(read)["content"] != "Reviewed replacement." {
		t.Fatalf("replacement=%#v %v", read, err)
	}
}

func TestSkillPublishUsesFrozenBytesAndPreservesChangedAuthorCopy(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "frozen-reviewed-package"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Reviewed bytes.")); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Reviewed resource."); err != nil {
		t.Fatal(err)
	}
	revision := reviewedSkillRevisionForTest(t, fixture, name)
	resource, err := fixture.call("host.skills.read", name, "references/guide.md")
	if err != nil || mapValue(resource)["draft_revision"] != revision || fmt.Sprint(mapValue(resource)["files"]) != "[SKILL.md references/guide.md]" {
		t.Fatalf("host resource read did not bind full inventory to its revision: %#v %v", resource, err)
	}
	draft, _, _ := fixture.server.findCompatibilitySkillDraft(name)
	authorPath := filepath.Join(filepath.Dir(draft.Path), "references", "guide.md")
	fixture.server.skillMutationMu.Lock()
	installed, err := fixture.server.publishCompatibilitySkillDraftWithCommit(name, false, revision, func(commit func() error) error {
		// Simulate an out-of-band edit after source capture but before promotion.
		if err := os.WriteFile(authorPath, []byte("Later author edit."), 0o600); err != nil {
			return err
		}
		return commit()
	})
	fixture.server.skillMutationMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(filepath.Join(filepath.Dir(installed.Path), "references", "guide.md"))
	if err != nil || string(live) != "Reviewed resource." {
		t.Fatalf("promotion reread live draft: %q %v", live, err)
	}
	author, err := os.ReadFile(authorPath)
	if err != nil || string(author) != "Later author edit." {
		t.Fatalf("cleanup lost changed author copy: %q %v", author, err)
	}
}

func TestSkillPublishHTTPRevisionAndExplicitReadSource(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "http-reviewed-package"
	if _, err := fixture.server.importSkillBundle(name+".md", "", false, []byte(kernelSkillManifestForTest(name, "Published original."))); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Reviewed draft.", "Published original."); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Reviewed resource."); err != nil {
		t.Fatal(err)
	}
	handler, user := fixture.server.Handler(), fixture.identity.access.UserID
	read := compatJSONRequest(t, handler, http.MethodGet, "/api/skills/catalog/"+name+"/content?source=draft", user, nil, http.StatusOK)
	revision := stringValue(read["draft_revision"])
	if read["origin"] != "draft" || !skillDraftRevisionPattern.MatchString(revision) {
		t.Fatalf("draft read=%#v", read)
	}
	resource := compatJSONRequest(t, handler, http.MethodGet, "/api/skills/catalog/"+name+"/content?source=draft&path=references%2Fguide.md", user, nil, http.StatusOK)
	if resource["content"] != "Reviewed resource." || resource["draft_revision"] != revision || fmt.Sprint(resource["files"]) != "[SKILL.md references/guide.md]" || fmt.Sprint(resource["files"]) != fmt.Sprint(read["files"]) {
		t.Fatalf("resource read mixed snapshot inventory/identity: %#v", resource)
	}
	live := compatJSONRequest(t, handler, http.MethodGet, "/api/skills/catalog/"+name+"/content?source=published", user, nil, http.StatusOK)
	if live["draft_revision"] != nil || !strings.Contains(stringValue(live["content"]), "Published original.") {
		t.Fatalf("published read substituted draft: %#v", live)
	}
	files := compatJSONRequest(t, handler, http.MethodGet, "/api/skills/catalog/"+name+"/files?source=published", user, nil, http.StatusOK)
	if strings.Contains(fmt.Sprint(files["files"]), "guide.md") {
		t.Fatalf("published inventory substituted draft: %#v", files)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Changed resource.", "Reviewed resource."); err != nil {
		t.Fatal(err)
	}
	conflict := compatJSONRequest(t, handler, http.MethodPost, "/api/skills/"+name+"/publish", user, map[string]any{"overwrite": true, "expected_draft_revision": revision}, http.StatusConflict)
	if conflict["code"] != "draft_revision_conflict" {
		t.Fatalf("untyped conflict=%#v", conflict)
	}
	current := compatJSONRequest(t, handler, http.MethodGet, "/api/skills/catalog/"+name+"/content?source=draft", user, nil, http.StatusOK)
	compatJSONRequest(t, handler, http.MethodPost, "/api/skills/"+name+"/publish", user, map[string]any{"overwrite": true, "expected_draft_revision": current["draft_revision"]}, http.StatusOK)
	compatJSONRequest(t, handler, http.MethodGet, "/api/skills/catalog/"+name+"/content?source=draft", user, nil, http.StatusNotFound)
	compatJSONRequest(t, handler, http.MethodGet, "/api/skills/catalog/"+name+"/files?source=draft", user, nil, http.StatusNotFound)
}
