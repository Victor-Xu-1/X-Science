package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kernelruntime "x-science/internal/kernel"
)

func TestSkillPublishUnknownAcknowledgementKeepsReadersCoherent(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		label := "installed_then_ack_lost"
		if rejected {
			label = "authority_rejected_before_install"
		}
		t.Run(label, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name = "publication-projection-fixture"
			manifest := kernelSkillManifestForTest(name, "Reviewed synthetic instructions.")
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Reviewed resource."); err != nil {
				t.Fatal(err)
			}
			revision := reviewedSkillRevisionForTest(t, fixture, name)
			guard, err := fixture.server.kernelSkillMutationCommit(context.Background(), fixture.bound, fixture.identity.access)
			if err != nil {
				t.Fatal(err)
			}
			if rejected {
				fixture.generation++
			}
			promotions, guardSucceeded := 0, false
			fixture.server.skillMutationMu.Lock()
			published, publishErr := fixture.server.publishCompatibilitySkillDraftWithCommit(name, false, revision, func(promote func() error) error {
				if err := guard(func() error {
					promotions++
					return promote()
				}); err != nil {
					return err
				}
				// Inject only after the original guard really returned success.
				// CommitAttempted or mere directory existence is not that proof.
				guardSucceeded = true
				return kernelruntime.NewHostCallError("outcome_unknown", "Synthetic acknowledgement loss after the original guard returned.")
			})
			fixture.server.skillMutationMu.Unlock()
			// Restore fixture-only authority so original readers can inspect a rejection.
			fixture.generation = 1
			expectedCode, expectedPromotions := "outcome_unknown", 1
			if rejected {
				expectedCode, expectedPromotions = "permission_denied", 0
			}
			requireSkillRevisionFailureForTest(t, publishErr, expectedCode)
			if published.Name != "" || promotions != expectedPromotions || guardSucceeded == rejected {
				t.Fatalf("unexpected completion: skill=%#v promotions=%d guardSucceeded=%t", published, promotions, guardSucceeded)
			}
			draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
			if err != nil || !found {
				t.Fatalf("uncertain/rejected publication lost author draft: found=%t err=%v", found, err)
			}
			snapshot, err := captureSkillDraftSnapshot(draft)
			if err != nil || snapshot.revision != revision {
				t.Fatalf("retained draft changed: revision=%q err=%v", snapshot.revision, err)
			}
			if rejected {
				if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
					t.Fatal("rejected publication became discoverable")
				}
				if _, err := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", name)); !os.IsNotExist(err) {
					t.Fatalf("rejected publication changed installed files: %v", err)
				}
				if _, err := fixture.server.executeSkillTool(map[string]any{"skill": name}); err == nil {
					t.Fatal("rejected draft became callable")
				}
				return
			}
			requirePublishedSkillProjectionReaders(t, fixture, name, manifest)
		})
	}
}

func requirePublishedSkillProjectionReaders(t *testing.T, fixture *kernelSkillDraftFixture, name, manifest string) {
	t.Helper()
	installed := filepath.Join(fixture.server.fileRoot, "skills", name, "SKILL.md")
	content, err := os.ReadFile(installed)
	if err != nil || string(content) != manifest {
		t.Fatalf("original promotion did not install reviewed bytes: %q %v", content, err)
	}
	read, err := fixture.call("host.skills.read", "local:"+name)
	if err != nil || mapValue(read)["content"] != manifest || mapValue(read)["draft_revision"] != nil {
		t.Fatalf("installed publication is not visible to explicit host read: %#v %v", read, err)
	}
	resource, err := fixture.call("host.skills.read", "local:"+name, "references/guide.md")
	if err != nil || mapValue(resource)["content"] != "Reviewed resource." {
		t.Fatalf("installed resource is not readable: %#v %v", resource, err)
	}
	catalog := compatJSONRequest(t, fixture.server.Handler(), http.MethodGet, "/api/skills/catalog",
		fixture.identity.access.UserID, nil, http.StatusOK)
	if entry := compatibilityCatalogEntryByName(t, catalog, name); entry["skillId"] != "local:"+name {
		t.Fatalf("HTTP catalog omitted installed identity: %#v", entry)
	}
	loaded, err := fixture.server.executeSkillTool(map[string]any{"skill": name})
	if err != nil || !strings.Contains(stringValue(mapValue(loaded)["prompt"]), "Reviewed synthetic instructions.") {
		t.Fatalf("installed Skill is not callable through the original loader: %#v %v", loaded, err)
	}
}
