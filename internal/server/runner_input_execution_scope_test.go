package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	transcriptstore "synon-go/internal/persistence/transcript"
	"synon-go/internal/sciencecapability"
	"synon-go/internal/skills"
)

func TestFollowOnIntentDoesNotInheritExecutionChoice(t *testing.T) {
	for _, text := range []string{
		"Continue with a different analysis using the previously saved inputs.",
		"继续当前工作，增加另一种分析，保留原始文件。",
	} {
		t.Run(text, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			payload, err := json.Marshal(map[string]any{
				"status": "completed", "toolPhase": "completed", "toolName": "manage_environments",
				"toolResult": map[string]any{"ok": true, "implementation_selection": map[string]any{
					"provenance": "registry-unique-local-pack", "implementations": []string{"First Engine"},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := f.repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
				Claim: f.claim, ClientMessageID: "first-selection", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: payload,
			}); err != nil {
				t.Fatal(err)
			}
			run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
			entries, err := f.server.runnerAskUserSelectionEntries(context.Background(), run)
			if err != nil || !reflect.DeepEqual(selectedAskUserImplementationsFromRunnerEntries(entries), []string{"First Engine"}) {
				t.Fatalf("initial selection=%#v err=%v", entries, err)
			}
			if _, _, created, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
				StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "follow-on-input",
				FrameEventID: "follow-on-event", MessageUUID: "follow-on-message", Text: text,
			}); err != nil || !created {
				t.Fatalf("append new intent created=%t err=%v", created, err)
			}
			intent, found, err := f.repo.GetActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID)
			if err != nil || !found || intent.Text != text {
				t.Fatalf("current intent=%#v found=%t err=%v", intent, found, err)
			}
			run.TaskIntentID = intent.ID
			boundary, required, err := f.server.sessionRunnerDurableTaskBoundary(context.Background(), run)
			if err != nil || !required || boundary != intent.SourceEventID {
				t.Fatalf("follow-on execution boundary=%q want=%q required=%t err=%v", boundary, intent.SourceEventID, required, err)
			}
			entries, err = f.server.runnerAskUserSelectionEntries(context.Background(), run)
			if err != nil || len(selectedAskUserImplementationsFromRunnerEntries(entries)) != 0 {
				t.Fatalf("new intent inherited prior engine: %#v err=%v", entries, err)
			}
			f.server.skillCatalog, f.server.scienceCapabilities = skills.NewCatalog(), &sciencecapability.Catalog{}
			addSelectionRouteEngine(f.server, "first-analysis", "first-skill", "First Engine")
			addSelectionRouteEngine(f.server, "second-analysis", "second-skill", "Second Engine")
			run.TaskIntent = intent.Text
			run.addRequiredScientificCapabilities("second-analysis")
			ctx := withTranscriptRunnerChatRun(context.Background(), run)
			if got := f.server.canonicalManagedEnvironmentImplementation(ctx, "Second Engine"); got != "Second Engine" ||
				!reflect.DeepEqual(run.selectedImplementationsSnapshot(), []string{"Second Engine"}) {
				t.Fatalf("current registered capability could not select its own route: got=%q selected=%v", got, run.selectedImplementationsSnapshot())
			}
		})
	}
}

func TestExplicitResumePreservesExecutionIntent(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	before, found, err := f.repo.GetActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("initial intent found=%t err=%v", found, err)
	}
	if _, _, created, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "resume-input",
		FrameEventID: "resume-event", MessageUUID: "resume-message", Text: "继续",
	}); err != nil || !created {
		t.Fatalf("append resume created=%t err=%v", created, err)
	}
	after, found, err := f.repo.GetActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil || !found || before.ID != after.ID {
		t.Fatalf("explicit resume split task: before=%#v after=%#v found=%t err=%v", before, after, found, err)
	}
	boundary, required, err := f.server.sessionRunnerDurableTaskBoundary(context.Background(), &sessionRunnerChatRun{
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}, TaskIntentID: after.ID,
	})
	if err != nil || !required || boundary != before.SourceEventID {
		t.Fatalf("resume lost original boundary=%q required=%t err=%v", boundary, required, err)
	}
}
