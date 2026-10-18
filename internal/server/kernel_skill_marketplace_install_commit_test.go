package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
)

func TestKernelSkillMarketplaceInstallCommitsWithSharedSingleConnectionPool(t *testing.T) {
	// newKernelSkillDraftFixture uses Workspace.TranscriptRepository, backed by
	// the same Store pool configured with MaxOpenConns(1), not a second DB.
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "marketplace-live-bound-install"
	setKernelSkillMarketplaceInstallTransport(t, fixture, name, nil)
	result, err := callKernelSkillMarketplaceInstall(t, fixture, name)
	if err != nil {
		t.Fatalf("bound marketplace install=%#v err=%v", result, err)
	}
	assertMarketplaceSkillPublished(t, fixture, name)
}

func TestMarketplaceInstallFinalGuardRejectsAfterPreparation(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "marketplace-final-guard"
	setKernelSkillMarketplaceInstallTransport(t, fixture, name, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	guard, err := fixture.server.kernelSkillMutationCommit(ctx, fixture.bound, fixture.bound.access)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, err := fixture.server.importMarketplaceSkills(ctx, fixture.bound.access.UserID, skillMarketplaceImportInput{
		Repo: "acme/skills", SHA: marketplaceTestSHA, Skills: []string{name},
	}, func(publish func() error) error {
		calls++
		staging, err := filepath.Glob(filepath.Join(privateSkillImportDirectory(filepath.Join(fixture.server.fileRoot, "skills")), ".skill-import-*"))
		if err != nil || len(staging) != 1 {
			t.Fatalf("final guard was not reached after preparation: staging=%v err=%v", staging, err)
		}
		fixture.generation++
		return guard(publish)
	})
	if calls != 1 {
		t.Fatalf("final guard calls=%d", calls)
	}
	assertKernelSkillMarketplaceInstallDenied(t, fixture, name, result, err)
}

func TestMarketplaceInstallPreservesPublicationWhenCommitAcknowledgementIsUnknown(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "marketplace-unknown-commit"
	setKernelSkillMarketplaceInstallTransport(t, fixture, name, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard, err := fixture.server.kernelSkillMutationCommit(ctx, fixture.bound, fixture.bound.access)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, err := fixture.server.importMarketplaceSkills(ctx, fixture.bound.access.UserID, skillMarketplaceImportInput{
		Repo: "acme/skills", SHA: marketplaceTestSHA, Skills: []string{name},
	}, func(publish func() error) error {
		return guard(func() error {
			calls++
			if err := publish(); err != nil {
				return err
			}
			// All external publication has completed. Cancel the exact context
			// before RunImmediateWithOutcome attempts COMMIT, not before IO.
			cancel()
			return nil
		})
	})
	var hostError *kernelruntime.HostCallError
	if !errors.As(err, &hostError) || hostError.Code != "outcome_unknown" || calls != 1 {
		t.Fatalf("unknown commit result=%#v calls=%d err=%v", result, calls, err)
	}
	assertMarketplaceSkillPublished(t, fixture, name)
}

func TestMarketplaceOperatorImportRetainsPartialConflictAndRecordsOnlyPublishedSkills(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const existing = "marketplace-existing"
	const fresh = "marketplace-fresh"
	manifest := kernelSkillManifestForTest(existing, "Preserve the existing package.")
	if _, err := fixture.server.importSkillBundle(existing+".md", "", false, []byte(manifest)); err != nil {
		t.Fatal(err)
	}
	setMarketplaceBatchTransport(t, fixture, map[string]string{
		existing: kernelSkillManifestForTest(existing, "Must not overwrite."),
		fresh:    kernelSkillManifestForTest(fresh, "New operator package."),
	})
	result := compatJSONRequest(t, fixture.server.Handler(), http.MethodPost, "/api/marketplace/import",
		fixture.bound.access.UserID, map[string]any{
			"repo": "acme/skills", "sha": marketplaceTestSHA, "skills": []string{existing, fresh},
		}, http.StatusCreated)
	imported, ok := result["imported"].([]any)
	if !ok || len(imported) != 1 || imported[0] != fresh {
		t.Fatalf("partial import=%#v", result)
	}
	skipped, ok := result["skipped"].([]any)
	if !ok || len(skipped) != 1 || mapValue(skipped[0])["name"] != existing {
		t.Fatalf("partial conflicts=%#v", result)
	}
	raw, err := os.ReadFile(filepath.Join(fixture.server.fileRoot, "skills", existing, "SKILL.md"))
	if err != nil || string(raw) != manifest {
		t.Fatalf("existing package changed: %q err=%v", raw, err)
	}
	assertMarketplaceSkillPublished(t, fixture, fresh)
	history, err := fixture.server.loadWebSkillImportHistory(fixture.bound.access.UserID)
	if err != nil || len(history) != 2 {
		t.Fatalf("partial history=%#v err=%v", history, err)
	}
}

func TestMarketplaceImportMetadataFailureRollsBackWholePublishedBatch(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	names := []string{"marketplace-batch-a", "marketplace-batch-b"}
	setMarketplaceBatchTransport(t, fixture, map[string]string{
		names[0]: kernelSkillManifestForTest(names[0], "First package."),
		names[1]: kernelSkillManifestForTest(names[1], "Second package."),
	})
	if _, err := fixture.server.settingsStore.Set("unchanged", "original"); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(fixture.server.fileRoot, "settings.json")
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	// The existing settings writer's temporary-file creation fails after both
	// directories have been promoted; no production fault-injection API needed.
	if err := os.Mkdir(settingsPath+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.server.importMarketplaceSkills(context.Background(), fixture.bound.access.UserID,
		skillMarketplaceImportInput{Repo: "acme/skills", SHA: marketplaceTestSHA, Skills: names}, immediateSkillDraftCommit)
	if err == nil {
		t.Fatal("metadata persistence failure was hidden")
	}
	after, readErr := os.ReadFile(settingsPath)
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed metadata batch changed settings: err=%v", readErr)
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", name)); !os.IsNotExist(err) {
			t.Errorf("failed batch retained published directory %s: %v", name, err)
		}
		if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
			t.Errorf("failed batch exposed %s in catalog", name)
		}
	}
	sources, err := fixture.server.loadWebSkillMarketplaceSources(fixture.bound.access.UserID)
	if err != nil || len(sources) != 0 {
		t.Fatalf("failed batch source=%#v err=%v", sources, err)
	}
	history, err := fixture.server.loadWebSkillImportHistory(fixture.bound.access.UserID)
	if err != nil || len(history) != 0 {
		t.Fatalf("failed batch history=%#v err=%v", history, err)
	}
}

func setMarketplaceBatchTransport(t *testing.T, fixture *kernelSkillDraftFixture, manifests map[string]string) {
	t.Helper()
	files := make(map[string]string, len(manifests))
	for name, manifest := range manifests {
		files["skills-main/skills/"+name+"/SKILL.md"] = manifest
	}
	archive := marketplaceSkillArchive(t, files)
	setKernelSkillMarketplaceInstallTransport(t, fixture, "unused-single-package", nil)
	transport := fixture.server.httpClient.Transport
	fixture.server.httpClient.Transport = marketplaceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(request)
		if err == nil && request.URL.Host == "codeload.github.com" {
			_ = response.Body.Close()
			response.Body = io.NopCloser(bytes.NewReader(archive))
		}
		return response, err
	})
}

func assertMarketplaceSkillPublished(t *testing.T, fixture *kernelSkillDraftFixture, name string) {
	t.Helper()
	root := filepath.Join(fixture.server.fileRoot, "skills", name)
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); !found {
		t.Fatal("committed Skill missing from runtime catalog")
	}
	sources, err := fixture.server.loadWebSkillMarketplaceSources(fixture.bound.access.UserID)
	if err != nil || len(sources) != 1 || len(sources[0].Skills) != 1 || sources[0].Skills[0] != name {
		t.Fatalf("committed source=%#v err=%v", sources, err)
	}
	filesystem, err := openSkillImportFilesystem(fixture.server.fileRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	defer filesystem.close()
	directory, err := filesystem.openDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	hash, err := webSkillDirectoryHash(directory)
	if err != nil || hash != sources[0].Hashes[name] {
		t.Fatalf("published hash=%q source hash=%q err=%v", hash, sources[0].Hashes[name], err)
	}
	history, err := fixture.server.loadWebSkillImportHistory(fixture.bound.access.UserID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history {
		if entry.SkillName == name && strings.EqualFold(entry.Status, "imported") {
			return
		}
	}
	t.Fatalf("committed import history missing: %#v", history)
}
