package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerCompletionPublicationFailurePersistsOnlySafeStage(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	const reportPath = "out/report.md"
	write := writeAgentSaveArtifactsFile(t, fixture.projectPath, reportPath, "# Decision report\n\nSupported conclusion.\n")
	fixture.saveExecution(t, fixture.identity.access, fixture.projectPath, "exec-report", 1, write)
	input := map[string]any{
		"files": []any{reportPath}, "language": "text",
		"destination":       map[string]any{reportPath: "snapshot"},
		"human_description": "Save the decision report",
	}
	saved, err := fixture.server.executeAgentSaveArtifacts(
		fixture.toolContext(t, "save-report", input), fixture.identity, "save-report", input,
	)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := agentSaveArtifactResults(t, saved)
	if len(artifacts) != 1 {
		t.Fatalf("saved artifacts = %d, want 1", len(artifacts))
	}
	versionID := stringValue(artifacts[0]["version_id"])
	const privateDetail = "private synthetic snapshot failure"
	if _, err := fixture.db.ExecContext(t.Context(), `CREATE TRIGGER reject_completion_snapshot
		BEFORE UPDATE OF is_intermediate ON artifact_version_provenance
		WHEN OLD.is_intermediate = 1 AND NEW.is_intermediate = 0
		BEGIN SELECT RAISE(ABORT, '`+privateDetail+`'); END`); err != nil {
		t.Fatal(err)
	}
	authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	session := sessionstore.Session{ID: run.SessionID, Project: &sessionstore.Project{ID: fixture.stream.ProjectID}}
	model := &artifactReferenceRepairModel{content: "The decision report is ready.", testing: t}
	_, completionErr := fixture.server.runSessionAgentWithArtifactReferenceRepair(t.Context(), session, agentruntime.Engine{Model: model},
		agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Prepare a report."}}}, run, nil)
	var sqliteFailure interface{ Code() int }
	if !errors.As(completionErr, &sqliteFailure) || !strings.Contains(completionErr.Error(), privateDetail) || model.calls != 1 {
		t.Fatalf("model calls=%d completion error=%v", model.calls, completionErr)
	}
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	if err := fixture.server.settleSessionRunnerChatOutcome(t.Context(), SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID},
		result, &activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, session, nil, "", completionErr, ""); err != nil {
		t.Fatal(err)
	}
	terminal, found, err := fixture.repo.GetEventByClientMessageID(t.Context(), fixture.stream.UID, fixture.stream.OwnerID,
		transcriptRunnerClientMessageID(fixture.claim, "runner-finished"))
	if err != nil || !found || result.Status != "failed" || result.FinishEventID != terminal.EventID {
		t.Fatalf("terminal found=%t result=%#v err=%v", found, result, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(terminal.PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(terminal.PayloadJSON), privateDetail) ||
		payload["reason_code"] != "runner_execution_failed" || payload["failure_stage"] != "artifact_publication" {
		t.Fatalf("terminal reason=%v stage=%v; expected the existing reason and a safe publication stage", payload["reason_code"], payload["failure_stage"])
	}
	if _, found, err := fixture.repo.GetEventByClientMessageID(t.Context(), fixture.stream.UID, fixture.stream.OwnerID,
		transcriptRunnerClientMessageID(fixture.claim, "assistant-message")); err != nil || found {
		t.Fatalf("unaccepted assistant found=%t err=%v", found, err)
	}
	if intermediate, found, err := fixture.store.ArtifactVersionIntermediate(versionID); err != nil || !found || !intermediate {
		t.Fatalf("failed snapshot intermediate=%t found=%t err=%v", intermediate, found, err)
	}
}

func TestArtifactDraftIsHiddenUntilValidatedCompletionPublishesSameVersion(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	write := writeAgentSaveArtifactsFile(t, fixture.projectPath, "out/report.md", "# Decision report\n\nSupported conclusion.\n")
	fixture.saveExecution(t, fixture.identity.access, fixture.projectPath, "exec-draft-report", 1, write)
	input := map[string]any{
		"files": []any{"out/report.md"}, "language": "text",
		"destination":       map[string]any{"out/report.md": "snapshot"},
		"human_description": "Saving the completed decision report",
	}
	result, err := fixture.server.executeAgentSaveArtifacts(
		fixture.toolContext(t, "save-draft-report", input), fixture.identity, "save-draft-report", input,
	)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := agentSaveArtifactResults(t, result)
	if len(artifacts) != 1 || artifacts[0]["publication_state"] != "draft" ||
		artifacts[0]["artifact_ref"] != nil || artifacts[0]["markdown_link"] != nil {
		t.Fatalf("draft result=%#v", result)
	}
	artifactID := stringValue(artifacts[0]["artifact_id"])
	versionID := stringValue(artifacts[0]["version_id"])
	if listed, listErr := fixture.store.ListCompatibilityConversationArtifacts(
		context.Background(), "owner-save", "project-save", "frame-save", true,
	); listErr != nil || len(listed) != 0 {
		t.Fatalf("visible before completion=%#v err=%v", listed, listErr)
	}
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	commits := []transcriptstore.ArtifactReferenceInput{{
		ArtifactID: artifactID, VersionID: versionID, Relation: transcriptstore.ArtifactRelationProduced,
	}}
	if err := fixture.server.publishSessionRunnerCompletionArtifacts(
		context.Background(),
		sessionstore.Session{ID: "frame-save", Project: &sessionstore.Project{ID: "project-save"}},
		run, commits,
	); err != nil {
		t.Fatal(err)
	}
	if intermediate, found, stateErr := fixture.store.ArtifactVersionIntermediate(versionID); stateErr != nil || !found || intermediate {
		t.Fatalf("published intermediate=%t found=%t err=%v", intermediate, found, stateErr)
	}
	listed, listErr := fixture.store.ListCompatibilityConversationArtifacts(
		context.Background(), "owner-save", "project-save", "frame-save", true,
	)
	if listErr != nil || len(listed) != 1 || listed[0].VersionID != versionID || listed[0].IsIntermediate {
		t.Fatalf("visible after completion=%#v err=%v", listed, listErr)
	}
}

func TestArtifactDraftFromPartiallySuccessfulSaveBatchPublishesAtCompletion(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	write := writeAgentSaveArtifactsFile(t, fixture.projectPath, "out/report.md", "# Decision report\n\nSupported conclusion.\n")
	fixture.saveExecution(t, fixture.identity.access, fixture.projectPath, "exec-partial-report", 1, write)
	input := map[string]any{
		"files": []any{"out/report.md", "out/missing.csv"}, "language": "text",
		"destination": map[string]any{
			"out/report.md": "snapshot", "out/missing.csv": "snapshot",
		},
		"human_description": "Saving the completed report and data table",
	}
	result, err := fixture.server.executeAgentSaveArtifacts(
		fixture.toolContext(t, "save-partial-report", input), fixture.identity, "save-partial-report", input,
	)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := agentSaveArtifactResults(t, result)
	if len(artifacts) != 1 || len(anySliceValue(mapValue(result)["errors"])) != 1 {
		t.Fatalf("partial save result=%#v", result)
	}
	artifactID := stringValue(artifacts[0]["artifact_id"])
	versionID := stringValue(artifacts[0]["version_id"])
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	commits := []transcriptstore.ArtifactReferenceInput{{
		ArtifactID: artifactID, VersionID: versionID, Relation: transcriptstore.ArtifactRelationProduced,
	}}
	if err := fixture.server.publishSessionRunnerCompletionArtifacts(
		context.Background(),
		sessionstore.Session{ID: "frame-save", Project: &sessionstore.Project{ID: "project-save"}},
		run, commits,
	); err != nil {
		t.Fatal(err)
	}
	if intermediate, found, stateErr := fixture.store.ArtifactVersionIntermediate(versionID); stateErr != nil || !found || intermediate {
		t.Fatalf("published partial-save intermediate=%t found=%t err=%v", intermediate, found, stateErr)
	}
}

func TestArtifactDraftPublishesFromDurableCommitsWithoutProviderHistory(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	write := writeAgentSaveArtifactsFile(t, fixture.projectPath, "out/report.md", "# Preserved report\n\nLatest durable bytes.\n")
	fixture.saveExecution(t, fixture.identity.access, fixture.projectPath, "exec-compacted-draft", 1, write)
	input := map[string]any{
		"files": []any{"out/report.md", "out/missing.csv"}, "language": "text",
		"destination":       map[string]any{"out/report.md": "snapshot", "out/missing.csv": "snapshot"},
		"human_description": "Save a report with a missing companion",
	}
	result, err := fixture.server.executeAgentSaveArtifacts(fixture.toolContext(t, "save-compacted-draft", input), fixture.identity, "save-compacted-draft", input)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := agentSaveArtifactResults(t, result)
	if len(artifacts) != 1 || len(anySliceValue(mapValue(result)["errors"])) != 1 {
		t.Fatalf("partial save=%#v", result)
	}
	versionID := stringValue(artifacts[0]["version_id"])
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	commits, err := fixture.server.sessionRunnerArtifactCommitReferences(context.Background(), run)
	if err != nil || len(commits) != 1 || commits[0].VersionID != versionID {
		t.Fatalf("durable commits=%#v err=%v", commits, err)
	}
	if err := fixture.server.publishSessionRunnerCompletionArtifacts(context.Background(), sessionstore.Session{ID: "frame-save", Project: &sessionstore.Project{ID: "project-save"}}, run, commits); err != nil {
		t.Fatal(err)
	}
	listed, err := fixture.store.ListCompatibilityConversationArtifacts(context.Background(), "owner-save", "project-save", "frame-save", true)
	if err != nil || len(listed) != 1 || listed[0].VersionID != versionID || listed[0].IsIntermediate {
		t.Fatalf("published list=%#v err=%v", listed, err)
	}
}
