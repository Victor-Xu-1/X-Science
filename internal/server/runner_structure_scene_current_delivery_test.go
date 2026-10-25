package server

import (
	"context"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
)

func TestStructureSceneAdvisoryFollowsCanonicalCurrentDelivery(t *testing.T) {
	for _, test := range []struct {
		name           string
		currentOutputs int
		structures     bool
	}{
		{name: "no_current_artifacts"},
		{name: "current_non_structure_artifact", currentOutputs: 1},
		{name: "current_structure_delivery_still_warns", currentOutputs: 2, structures: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			f := newAgentSaveArtifactsFixture(t)
			session := sessionstore.Session{ID: f.stream.FrameID, Project: &sessionstore.Project{ID: f.stream.ProjectID}, WorkDir: f.projectPath}
			run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
			// Real successful save receipts, then an actually published prior input.
			const structure = "ATOM      1  N   ALA A   1       0.000   0.000   0.000  1.00  0.00           N\nEND\n"
			saveRoundPresentationFile(t, f, "historical-base", "historical-base.pdb", structure)
			saveRoundPresentationFile(t, f, "historical-derived", "historical-derived.pdb", structure)
			historical, err := f.server.sessionRunnerArtifactCommitReferences(ctx, run)
			if err != nil || len(historical) != 2 {
				t.Fatalf("historical commits=%#v error=%v", historical, err)
			}
			if err := f.server.publishSessionRunnerCompletionArtifacts(ctx, session, run, historical); err != nil {
				t.Fatal(err)
			}
			for _, ref := range historical {
				if intermediate, found, err := f.store.ArtifactVersionIntermediate(ref.VersionID); err != nil || !found || intermediate {
					t.Fatalf("history is not published: intermediate=%t found=%t error=%v", intermediate, found, err)
				}
			}
			previousInput := f.claim.ClaimedInputRevision
			finishRoundPresentation(t, f, "completed")
			startNextPresentationRound(t, f)
			if f.claim.ClaimedInputRevision <= previousInput {
				t.Fatal("fixture did not admit an independent input")
			}
			run = &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
			if test.structures {
				saveRoundPresentationFile(t, f, "current-base", "current-base.pdb", structure)
				saveRoundPresentationFile(t, f, "current-derived", "current-derived.pdb", structure)
			} else if test.currentOutputs > 0 {
				saveRoundPresentationFile(t, f, "current-note", "current-note.txt", "The reusable procedure was updated.\n")
			}
			commits, err := f.server.sessionRunnerArtifactCommitReferences(ctx, run)
			if err != nil || len(commits) != 2+test.currentOutputs {
				t.Fatalf("durable history changed: commits=%#v error=%v", commits, err)
			}
			const answer = "The current procedure update is complete."
			selected, err := f.server.sessionRunnerActiveArtifactCommitReferences(ctx, run, commits, answer)
			if err != nil || len(selected) != test.currentOutputs {
				t.Fatalf("canonical current delivery=%#v error=%v", selected, err)
			}
			for _, ref := range selected {
				for _, old := range historical {
					if ref.VersionID == old.VersionID {
						t.Fatal("canonical current input inherited a historical structure")
					}
				}
			}
			if test.structures {
				advisory, err := f.server.verifySessionRunnerVisualArtifactEvidence(session, selected)
				if err != nil || advisory == nil || advisory.Code != completionAdvisoryStructurePreview {
					t.Fatalf("current comparison lost its missing-manifest finding: advisory=%#v error=%v", advisory, err)
				}
				return
			}
			// Known model text contains no scene warning. Exercise the real host
			// completion consumer instead of interpreting a persisted UI sentence.
			model := &artifactReferenceRepairModel{content: answer, testing: t}
			result, err := f.server.runSessionAgentWithArtifactReferenceRepair(ctx, session,
				agentruntime.Engine{Model: model}, agentruntime.RunRequest{
					Messages: []agentruntime.Message{{Role: "user", Content: "Update the reusable procedure using existing evidence."}},
				}, run, nil)
			if err != nil {
				t.Fatalf("unrelated prior structures blocked completion: %v", err)
			}
			english, chinese := sessionRunnerCompletionAdvisoryText(completionAdvisoryStructurePreview)
			if model.calls != 1 || result.FinalMessage.Content != answer || strings.Contains(result.FinalMessage.Content, english) || strings.Contains(result.FinalMessage.Content, chinese) {
				t.Fatalf("host contaminated the current answer: calls=%d final=%q", model.calls, result.FinalMessage.Content)
			}
			if len(result.Messages) == 0 || result.Messages[len(result.Messages)-1].Content != result.FinalMessage.Content {
				t.Fatal("public final content and authoritative assistant message diverged")
			}
			// Exercise the existing deterministic completion-recovery consumer over
			// the same real claim/evidence, without claiming crash/restart coverage.
			advisories, err := f.server.validateRecoveredTranscriptCompletionCandidate(ctx, session,
				sessionRunnerCompletionRecoveryCandidate{Content: answer, SourceRun: run})
			if err != nil {
				t.Fatalf("completion-recovery validation inherited old obligations: %v", err)
			}
			for _, advisory := range advisories {
				if advisory != nil && advisory.Code == completionAdvisoryStructurePreview {
					t.Fatalf("completion recovery inherited a historical comparison: %#v", advisory)
				}
			}
		})
	}
}
