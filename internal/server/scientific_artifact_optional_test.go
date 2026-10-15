package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func invalidScientificValidatorManager(t *testing.T) *kernelruntime.Manager {
	t.Helper()
	root := t.TempDir()
	catalog := filepath.Join(root, "invalid-catalog.json")
	if err := os.WriteFile(catalog, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return kernelruntime.NewManager(kernelruntime.Config{
		CondaEnvsPath: filepath.Join(root, "envs"), CondaRuntimeCatalog: catalog,
		SDFValidatorPath: filepath.Join(root, "validator.py"),
	})
}

func TestOptionalMoleculeValidatorAbsenceDoesNotDiscardSavedArtifact(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	const path = "out/library.sdf"
	write := writeAgentSaveArtifactsFile(t, f.projectPath, path, validServerEthanolSDF)
	f.saveExecution(t, f.identity.access, f.projectPath, "molecular-source", 1, write)
	input := map[string]any{"files": []any{path}, "language": "python", "environment": "x-science-python", "human_description": "Save existing molecular data"}
	result, err := f.server.executeAgentSaveArtifacts(f.toolContext(t, "save-without-parser", input), f.identity, "save-without-parser", input)
	artifacts := agentSaveArtifactResults(t, result)
	if err != nil || result["errors"] != nil || len(artifacts) != 1 {
		t.Fatalf("optional validator prevented immutable save: result=%#v err=%v", result, err)
	}
	versionID := stringValue(artifacts[0]["version_id"])
	_, version, reader, found, err := f.store.OpenArtifactVersionContent(versionID)
	if err != nil || !found {
		t.Fatalf("saved version unavailable: found=%t err=%v", found, err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(data) != validServerEthanolSDF || version.ContentSHA256 == "" {
		t.Fatalf("original bytes were lost: read=%v close=%v", readErr, closeErr)
	}
	warnings := anySliceValue(result["warnings"])
	if len(warnings) != 1 {
		t.Fatalf("chemical limitation is not explicit: %#v", result)
	}
	warning := mapValue(warnings[0])
	if warning["code"] != "scientific_validation_unavailable" || warning["validation_status"] != "not_performed" || warning["version_id"] != versionID || warning["sha256"] != version.ContentSHA256 {
		t.Fatalf("missing precise unvalidated-artifact notice: %#v", warning)
	}
}

func optionalScientificCompletionFixture(t *testing.T) (*agentSaveArtifactsFixture, sessionstore.Session, *sessionRunnerChatRun, workspace.ArtifactVersion, string) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	intent, found, err := f.repo.EnsureActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("canonical intent: found=%t err=%v", found, err)
	}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	if err := applySessionRunnerTaskIntent(run, intent.Text, intent.Language, intent.ID, intent.Revision); err != nil {
		t.Fatal(err)
	}
	_, version := seedSessionRunnerSDFCommit(t, f, "molecular-data", "library.sdf", validServerEthanolSDF, transcriptstore.ArtifactRelationProduced)
	if err := f.store.SetArtifactRetentionMode(context.Background(), version.ArtifactID, f.stream.ProjectID, f.stream.OwnerID, "snapshot"); err != nil {
		t.Fatal(err)
	}
	session := sessionstore.Session{ID: f.stream.SessionID, Project: &sessionstore.Project{ID: f.stream.ProjectID}}
	content := "Existing molecular data: [library.sdf]({{artifact:" + version.ID + "}})."
	return f, session, run, version, content
}

func TestOptionalMoleculeValidatorAbsenceDeliversFreshAndRecoveredFinal(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		name := "fresh"
		if recovered {
			name = "recovered"
		}
		t.Run(name, func(t *testing.T) {
			f, session, run, _, content := optionalScientificCompletionFixture(t)
			final := ""
			if recovered {
				advisories, err := f.server.validateRecoveredTranscriptCompletionCandidate(context.Background(), session, sessionRunnerCompletionRecoveryCandidate{Content: content, SourceRun: run})
				if err != nil {
					t.Fatalf("missing optional parser blocked saved completion: %v", err)
				}
				final = appendSessionRunnerCompletionAdvisories(content, run.ResponseLanguage, advisories...)
			} else {
				model := &artifactReferenceRepairModel{content: content, testing: t}
				result, err := f.server.runSessionAgentWithArtifactReferenceRepair(context.Background(), session,
					agentruntime.Engine{Model: model}, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}}}, run, nil)
				if err != nil || model.calls != 1 {
					t.Fatalf("missing optional parser blocked final: calls=%d err=%v", model.calls, err)
				}
				final = result.FinalMessage.Content
				if len(result.Messages) == 0 || result.Messages[len(result.Messages)-1].Content != final {
					t.Fatal("canonical final and delivered answer differ")
				}
			}
			if !strings.HasPrefix(final, content) || strings.Count(final, "Chemical validation was not performed") != 1 {
				t.Fatalf("original content or honest notice missing: %q", final)
			}
		})
	}
}

func TestMolecularCompletionRejectsSameSizeStoredCorruptionBeforeOptionalValidator(t *testing.T) {
	f, session, run, version, content := optionalScientificCompletionFixture(t)
	corrupt := []byte(validServerEthanolSDF)
	corrupt[0] = 'X'
	if version.StoragePath == "" {
		if _, err := f.db.Exec("UPDATE artifact_versions SET content=? WHERE id=?", corrupt, version.ID); err != nil {
			t.Fatal(err)
		}
	} else {
		root := f.databasePath + ".blobs"
		path := filepath.Join(root, filepath.FromSlash(version.StoragePath))
		relative, err := filepath.Rel(root, path)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatal("fixture blob escaped its temporary store")
		}
		if err := os.WriteFile(path, corrupt, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.server.validateRecoveredTranscriptCompletionCandidate(context.Background(), session, sessionRunnerCompletionRecoveryCandidate{Content: content, SourceRun: run})
	if err == nil || errors.Is(err, errScientificArtifactValidationUnavailable) || !strings.Contains(err.Error(), "content digest mismatch") {
		t.Fatalf("stored corruption was not distinguished from optional validation: %v", err)
	}
}
