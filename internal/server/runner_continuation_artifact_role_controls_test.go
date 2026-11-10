package server

import (
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestRunnerContinuationCanonicalDualRolesRemainDistinct(t *testing.T) {
	fixture, run, source, body := downloadedContinuationSource(t)
	ctx := fixture.toolContext(t, "explicit-source-snapshot", map[string]any{"files": []any{"material.html"}, "language": "text"})
	parent, ok := transcriptArtifactRunFromContext(ctx)
	if !ok {
		t.Fatal("missing actual source checkpoint")
	}
	artifact, _, found, err := fixture.store.GetArtifactVersionMetadata(stringValue(source["version_id"]))
	if err != nil || !found {
		t.Fatalf("source MIME authority missing: found=%t err=%v", found, err)
	}
	_, version, err := fixture.store.WriteArtifactVersionRealtime(
		workspace.WithMutationIdempotencyKey(ctx, "canonical-dual-role"), workspace.WriteArtifactVersionInput{
			ArtifactID: stringValue(source["artifact_id"]), ProjectID: fixture.stream.ProjectID,
			Name: artifact.Name, ContentType: artifact.Kind, Content: strings.NewReader(body), MaxBytes: int64(len(body)),
			CreatedBy: fixture.claim.RunnerID, RootFrameID: fixture.stream.RootFrameID, FrameID: fixture.stream.FrameID,
			TranscriptAssociation: &workspace.ArtifactTranscriptAssociation{StreamUID: fixture.stream.UID,
				RunnerID: fixture.claim.RunnerID, ClaimToken: fixture.claim.ClaimToken, Attempt: fixture.claim.Attempt,
				SourceEventID: parent.SourceEventID, Relation: "produced", ReuseCurrentVersionIfUnchanged: true},
		}, fixture.stream.OwnerID)
	if err != nil || version.ID != source["version_id"] {
		t.Fatalf("fixture did not bind both real roles to one immutable version: %#v %v", version, err)
	}
	commits, err := fixture.server.sessionRunnerArtifactCommitReferences(t.Context(), run)
	if err != nil || len(commits) != 2 {
		t.Fatalf("canonical dual roles were collapsed: %#v err=%v", commits, err)
	}
	roles := map[transcriptstore.ArtifactRelation]bool{}
	for _, ref := range commits {
		roles[ref.Relation] = true
	}
	if !roles[transcriptstore.ArtifactRelationProduced] || !roles[transcriptstore.ArtifactRelationConsumed] {
		t.Fatalf("canonical produced or consumed role lost: %#v", commits)
	}
	failures, err := fixture.server.validateSessionRunnerCrossArtifactConsistency(t.Context(), fixture.stream.ProjectID, commits)
	if err != nil || !strings.Contains(strings.Join(failures, ","), "unresolved_template_marker:") {
		t.Fatalf("explicit canonical report lost the original template gate: %#v err=%v", failures, err)
	}
}

func TestRunnerContinuationRolesDoNotBorrowForeignAuthority(t *testing.T) {
	t.Run("owner", func(t *testing.T) {
		fixture, run, _, _ := downloadedContinuationSource(t)
		copyAuthority := *run.Transcript
		copyAuthority.Stream.OwnerID = "foreign-owner"
		run.Transcript = &copyAuthority
		if _, err := fixture.server.sessionRunnerArtifactCommitReferences(t.Context(), run); err == nil {
			t.Fatal("foreign owner borrowed the canonical consumed role")
		}
	})
	t.Run("branch", func(t *testing.T) {
		fixture, run, source, _ := downloadedContinuationSource(t)
		branch, err := fixture.repo.GetBranchState(t.Context(), fixture.stream.UID, fixture.stream.OwnerID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.repo.ForkFrameUserMessageBranch(t.Context(), transcriptstore.ForkFrameUserMessageBranchInput{
			StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, SourceBranchID: branch.ActiveBranchID,
			ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation,
			ClientMutationID: "source-role-branch", SourceClientMessageID: "save-user", SourceMessageIndex: 0,
			ReplacementText: "Create another unrelated artifact.", Destinations: []string{"ws"},
		}); err != nil {
			t.Fatal(err)
		}
		commits, err := fixture.server.sessionRunnerArtifactCommitReferences(t.Context(), run)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range commits {
			if ref.VersionID == source["version_id"] && ref.Relation == transcriptstore.ArtifactRelationConsumed {
				t.Fatalf("abandoned branch supplied source-role authority: %#v", commits)
			}
		}
	})
}

func TestRunnerContinuationSourceAllowsReportCompletion(t *testing.T) {
	fixture, run, source, _ := downloadedContinuationSource(t)
	run.SessionID, run.Attempt, run.ClaimToken = fixture.stream.SessionID, int(fixture.claim.Attempt), fixture.claim.ClaimToken
	intent, found, err := fixture.repo.EnsureActiveFrameTaskIntent(t.Context(), fixture.stream.UID, fixture.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("canonical intent found=%t err=%v", found, err)
	}
	if err := applySessionRunnerTaskIntent(run, intent.Text, intent.Language, intent.ID, intent.Revision); err != nil {
		t.Fatal(err)
	}
	write := writeAgentSaveArtifactsFile(t, fixture.projectPath, "out/retention_report.md", "# Retention report\nOriginal source input was preserved.\n")
	fixture.saveExecution(t, fixture.identity.access, fixture.projectPath, "retention-report-write", 1, write)
	input := map[string]any{"files": []any{"out/retention_report.md"}, "language": "text", "human_description": "Saving the completed report"}
	saved, err := fixture.server.executeAgentSaveArtifacts(fixture.toolContext(t, "save-retention-report", input), fixture.identity, "save-retention-report", input)
	if err != nil {
		t.Fatal(err)
	}
	report := agentSaveArtifactResults(t, saved)[0]
	model := &artifactReferenceRepairModel{content: "Completed report: {{artifact:" + stringValue(report["version_id"]) + "}}", testing: t}
	result, err := fixture.server.runSessionAgentWithArtifactReferenceRepair(t.Context(),
		sessionstore.Session{ID: run.SessionID, Project: &sessionstore.Project{ID: fixture.stream.ProjectID}},
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}}}, run, nil)
	if err != nil || model.calls != 1 || !strings.HasPrefix(result.FinalMessage.Content, model.content) {
		t.Fatalf("source input forced report correction: model_calls=%d err=%v", model.calls, err)
	}
	if intermediate, found, err := fixture.store.ArtifactVersionIntermediate(stringValue(source["version_id"])); err != nil || !found || !intermediate {
		t.Fatalf("raw source input became a published report: intermediate=%t found=%t err=%v", intermediate, found, err)
	}
}
