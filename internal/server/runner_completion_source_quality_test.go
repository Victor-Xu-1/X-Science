package server

import (
	"context"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestRecoveredSourceQualityNoticeMatchesFreshCompletion(t *testing.T) {
	const source = `{"sources":[{"id":"source-1","claims_supported":["observed_count"]}]}`
	f, session, run, _, _ := qualityArtifactCompletionFixture(t, "source_evidence.json", source)
	ctx := context.Background()
	commits, err := f.server.sessionRunnerArtifactCommitReferences(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := f.server.validateSessionRunnerSourceClaimEvidence(ctx, session.Project.ID, commits, nil)
	if err != nil || len(findings) != 1 || !strings.HasPrefix(findings[0], "source_claim_missing_attested_excerpt:") {
		t.Fatalf("expected a structurally valid unverified source fixture: %#v %v", findings, err)
	}
	model := &artifactReferenceRepairModel{content: "The saved source ledger has unverified provenance.", testing: t}
	fresh, err := f.server.runSessionAgentWithArtifactReferenceRepair(ctx, session,
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{
			Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}},
		}, run, nil)
	if err != nil || !strings.Contains(fresh.FinalMessage.Content, "Result quality is unverified") {
		t.Fatalf("fresh completion did not disclose the source finding: %q %v", fresh.FinalMessage.Content, err)
	}
	advisories, err := f.server.validateRecoveredTranscriptCompletionCandidate(ctx, session,
		sessionRunnerCompletionRecoveryCandidate{Content: model.content, SourceRun: run})
	if err != nil {
		t.Fatal(err)
	}
	recovered := appendSessionRunnerCompletionAdvisories(model.content, run.ResponseLanguage, advisories...)
	if recovered != fresh.FinalMessage.Content {
		t.Fatalf("recovery hid a source-quality finding: fresh=%q recovered=%q", fresh.FinalMessage.Content, recovered)
	}
}
