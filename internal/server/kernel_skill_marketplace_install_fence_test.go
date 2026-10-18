package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestKernelSkillMarketplaceInstallRequiresExecutionBinding(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "marketplace-unbound-install"
	network := setKernelSkillMarketplaceInstallTransport(t, fixture, name, nil)
	fixture.bound.transcriptExecution = kernelTranscriptExecutionBinding{}

	result, err := callKernelSkillMarketplaceInstall(t, fixture, name)
	assertKernelSkillMarketplaceInstallDenied(t, fixture, name, result, err)
	if network.requests != 0 {
		t.Errorf("missing execution binding reached marketplace network: %d requests", network.requests)
	}
}

func TestKernelSkillMarketplaceInstallRejectsRunnerReplacementDuringDownload(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "marketplace-stale-runner-install"
	replaced := false
	network := setKernelSkillMarketplaceInstallTransport(t, fixture, name, func(ctx context.Context) {
		interruption, err := fixture.repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{
			Claim: fixture.claim, ClientMessageID: "marketplace-runner-interruption",
			ReasonCode: "runtime_draining", Resumable: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		reclaimed, err := fixture.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
			StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
			RunnerID: fixture.claim.RunnerID, TTL: time.Minute,
			ResumeSource:     transcriptstore.ResumeSourceCheckpoint,
			ResumeCheckpoint: interruption.Checkpoint.Sequence,
		})
		if err != nil || !reclaimed.Claimed || reclaimed.Claim.ClaimToken == fixture.claim.ClaimToken {
			t.Fatalf("replacement claim=%#v err=%v", reclaimed, err)
		}
		replaced = true
	})

	result, err := callKernelSkillMarketplaceInstall(t, fixture, name)
	if !replaced || network.archives != 2 {
		t.Fatalf("did not replace the real runner during the import download: replaced=%t archives=%d result=%#v err=%v",
			replaced, network.archives, result, err)
	}
	assertKernelSkillMarketplaceInstallDenied(t, fixture, name, result, err)
}

func TestKernelSkillMarketplaceInstallRejectsKernelReplacementDuringDownload(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "marketplace-stale-kernel-install"
	network := setKernelSkillMarketplaceInstallTransport(t, fixture, name, func(context.Context) {
		// The transport and bound generation getter run synchronously on this
		// call, so this changes the real binding without a test data race.
		fixture.generation++
	})

	result, err := callKernelSkillMarketplaceInstall(t, fixture, name)
	if fixture.generation != 2 || network.archives != 2 {
		t.Fatalf("did not replace the kernel during the import download: generation=%d archives=%d result=%#v err=%v",
			fixture.generation, network.archives, result, err)
	}
	assertKernelSkillMarketplaceInstallDenied(t, fixture, name, result, err)
}

func newKernelSkillMarketplaceInstallFixture(t *testing.T) *kernelSkillDraftFixture {
	t.Helper()
	fixture := newKernelSkillDraftFixture(t)
	seedPermissionTestAgent(t, fixture.store, fixture.bound.access.UserID)
	if _, err := fixture.store.SetFrameRuntimeMetadata(fixture.bound.access.Frame.ID, workspace.FrameRuntimeMetadata{
		FrameID: fixture.bound.access.Frame.ID,
		ContextData: map[string]any{"web_assistant": map[string]any{
			"id":                     "xscience:operon",
			"conversation_overrides": map[string]any{"permission": "allow"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return fixture
}

type kernelSkillMarketplaceInstallNetwork struct {
	requests int
	archives int
}

func setKernelSkillMarketplaceInstallTransport(
	t *testing.T,
	fixture *kernelSkillDraftFixture,
	name string,
	duringImportDownload func(context.Context),
) *kernelSkillMarketplaceInstallNetwork {
	t.Helper()
	archive := marketplaceSkillArchive(t, map[string]string{
		"skills-main/skills/" + name + "/SKILL.md": kernelSkillManifestForTest(name, "Reviewed marketplace workflow."),
	})
	network := &kernelSkillMarketplaceInstallNetwork{}
	fixture.server.httpClient = &http.Client{Transport: marketplaceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		network.requests++
		contentType := "application/json"
		var reader io.ReadCloser
		switch {
		case request.URL.Host == "api.github.com" && strings.Contains(request.URL.Path, "/commits/"):
			reader = io.NopCloser(strings.NewReader("{\"sha\":\"" + marketplaceTestSHA + "\"}"))
		case request.URL.Host == "api.github.com" && strings.HasSuffix(request.URL.Path, "/license"):
			reader = io.NopCloser(strings.NewReader("{\"license\":{\"spdx_id\":\"Apache-2.0\"}}"))
		case request.URL.Host == "codeload.github.com":
			network.archives++
			// The first archive belongs to preview; the second is the actual
			// pinned import after approval and before filesystem publication.
			if network.archives == 2 && duringImportDownload != nil {
				duringImportDownload(request.Context())
			}
			contentType = "application/zip"
			reader = io.NopCloser(bytes.NewReader(archive))
		default:
			return nil, fmt.Errorf("unexpected marketplace request: %s", request.URL.Redacted())
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK),
			Header: http.Header{"Content-Type": []string{contentType}},
			Body:   reader, Request: request,
		}, nil
	})}
	return network
}

func callKernelSkillMarketplaceInstall(t *testing.T, fixture *kernelSkillDraftFixture, name string) (any, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return fixture.server.handleKernelHostCall(fixture.bound, nil)(ctx, kernelruntime.HostCall{
		ID: "hc-00000000000000000000000000000001", CellID: "skill-authoring-cell",
		Method: "host.skills.install",
		Args: []any{map[string]any{
			"repo": "acme/skills", "sha": marketplaceTestSHA, "skills": []any{name},
		}},
	})
}

func assertKernelSkillMarketplaceInstallDenied(
	t *testing.T, fixture *kernelSkillDraftFixture, name string, result any, err error,
) {
	t.Helper()
	var hostError *kernelruntime.HostCallError
	if !errors.As(err, &hostError) || hostError.Code != "permission_denied" {
		t.Errorf("stale marketplace installation must return permission_denied: result=%#v err=%v", result, err)
	}
	if _, statErr := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", name)); !os.IsNotExist(statErr) {
		t.Errorf("unauthorized marketplace directory was published: %v", statErr)
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
		t.Error("unauthorized marketplace Skill entered the runtime catalog")
	}
	sources, loadErr := fixture.server.loadWebSkillMarketplaceSources(fixture.bound.access.UserID)
	if loadErr != nil || len(sources) != 0 {
		t.Errorf("unauthorized marketplace source persisted: %#v err=%v", sources, loadErr)
	}
	history, loadErr := fixture.server.loadWebSkillImportHistory(fixture.bound.access.UserID)
	if loadErr != nil || len(history) != 0 {
		t.Errorf("unauthorized marketplace import history persisted: %#v err=%v", history, loadErr)
	}
}
