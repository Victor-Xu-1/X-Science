package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func TestSealedScientificSelectionGuardsAuthorityAndCompleteness(t *testing.T) {
	for _, name := range []string{"sealed", "owner", "branch", "input", "epoch", "watermark", "condition", "terminal", "unbound", "deleted", "late source", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			var beforeFinish []func(*agentSaveArtifactsFixture)
			if name == "late source" {
				beforeFinish = append(beforeFinish, func(f *agentSaveArtifactsFixture) {
					seedSessionRunnerSDFCommit(t, f, "later-molecule", "later.sdf", validServerEthanolSDF, transcriptstore.ArtifactRelationProduced)
				})
			}
			f, _, run, version, _ := knownInvalidScientificContinuationFixture(t, beforeFinish...)
			ctx := context.Background()
			candidate, found, err := f.server.loadTranscriptCompletionRecoveryCandidate(ctx, run.Transcript)
			if err != nil || !found || candidate.ScientificSelectionSource == nil {
				t.Fatalf("canonical source unavailable: found=%t err=%v", found, err)
			}
			proof := *candidate.ScientificSelectionSource
			wantError, wantComplete := false, true
			switch name {
			case "owner":
				proof.OwnerID, wantError = "different-owner", true
			case "branch":
				proof.Projection.BranchGeneration++
				wantError = true
			case "input":
				proof.InputRevision++
				wantError = true
			case "epoch":
				proof.Epoch++
				wantError = true
			case "watermark":
				proof.Projection.ThroughPublicationSequence--
				wantError = true
			case "condition":
				proof.CorrectionCondition = (&sessionRunnerReferenceIntegrityError{InvalidScientificArtifacts: []string{"invalid_smiles_records"}}).runnerCorrection().Condition
				wantError = true
			case "terminal":
				proof.TerminalEventID = proof.CorrectionEventID
				wantComplete = false
			case "unbound":
				if _, err := f.db.Exec(`UPDATE transcript_artifact_commits SET bound_event_id=NULL WHERE version_id=?`, version.ID); err != nil {
					t.Fatal(err)
				}
				wantComplete = false
			case "deleted":
				if err := f.store.DeleteArtifact(version.ArtifactID); err != nil {
					t.Fatal(err)
				}
			case "late source":
				wantComplete = false
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx, wantError = cancelled, true
			}
			sealed, complete, err := f.repo.SealedFailedArtifactCommitSnapshot(ctx, proof)
			if (err != nil) != wantError || (!wantError && complete != wantComplete) {
				t.Fatalf("sealed history authority: complete=%t expected=%t err=%v wantError=%t", complete, wantComplete, err, wantError)
			}
			if complete && !wantError {
				if len(sealed.Snapshot.References) != 1 || len(sealed.InputReferences) != 1 || sealed.Snapshot.References[0].VersionID != version.ID {
					t.Fatalf("immutable current head or input scope lost: %#v", sealed)
				}
				if name == "deleted" && sealed.Snapshot.References[0].Availability != transcriptstore.ArtifactDeleted {
					t.Fatalf("deleted selected member disappeared instead of retaining tombstone: %#v", sealed)
				}
			}
		})
	}
}

func TestSealedScientificSelectionDoesNotPoisonReplacement(t *testing.T) {
	f, session, run, previous, _ := knownInvalidScientificContinuationFixture(t)
	f.claim = run.Transcript.Claim
	_, replacement := seedSessionRunnerSDFCommit(t, f, previous.ArtifactID, "library.sdf", validServerEthanolSDF+"\n\n", transcriptstore.ArtifactRelationProduced)
	if replacement.ID == previous.ID || replacement.ContentSHA256 == previous.ContentSHA256 {
		t.Fatal("replacement must have distinct immutable bytes and identity")
	}
	content := "Replacement data: [library.sdf]({{artifact:" + replacement.ID + "}})."
	model := &artifactReferenceRepairModel{content: content, testing: t}
	result, err := f.server.runSessionAgentWithArtifactReferenceRepair(context.Background(), session,
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}}}, run, nil)
	if err != nil || model.calls != 1 || !strings.HasPrefix(result.FinalMessage.Content, content) ||
		!strings.Contains(result.FinalMessage.Content, "Earlier chemical-validation failures could not be matched") {
		t.Fatalf("replacement was poisoned by old invalidity or falsely certified: final=%q err=%v", result.FinalMessage.Content, err)
	}
}

func TestScientificHistoryNoticeSharesLocalizedIdempotentFinal(t *testing.T) {
	advice := &sessionRunnerCompletionAdvisory{Code: completionAdvisoryChemicalHistory, Findings: []string{"condition:historical_selection_unverified"}}
	for _, language := range []string{"", "en", "zh-CN"} {
		want := "Earlier chemical-validation failures"
		if sessionRunnerRequiresChinese(language) {
			want = "先前的化学结构校验失败"
		}
		text := appendSessionRunnerCompletionAdvisories("Existing data: 12.5", language, advice, advice)
		if !strings.HasPrefix(text, "Existing data: 12.5") || strings.Count(text, want) != 1 || appendSessionRunnerCompletionAdvisories(text, language, advice) != text {
			t.Fatalf("history notice not localized or idempotent: %q", text)
		}
	}
}

func TestScientificHistoryDoesNotBindRenamedMemberToAnotherArtifact(t *testing.T) {
	for _, test := range []struct {
		name        string
		peer        bool
		wantInvalid bool
	}{{"renamed.txt", true, false}, {"renamed.smi", true, false}, {"renamed.smi", false, false}, {"library.sdf", true, true}} {
		name := test.name
		if test.peer {
			name += " with peer"
		}
		t.Run(name, func(t *testing.T) {
			var peer workspace.ArtifactVersion
			var setup func(*agentSaveArtifactsFixture)
			if test.peer {
				setup = func(f *agentSaveArtifactsFixture) {
					_, peer = seedSessionRunnerSDFCommit(t, f, "valid-peer", "peer.sdf", validServerEthanolSDF, transcriptstore.ArtifactRelationProduced)
				}
			}
			f, session, run, rejected, _ := knownInvalidScientificContinuationFixtureWithSetup(t, setup)
			if _, err := f.store.RenameArtifact(rejected.ArtifactID, test.name); err != nil {
				t.Fatal(err)
			}
			_, unchanged, found, err := f.store.GetArtifactVersionMetadata(rejected.ID)
			if err != nil || !found || unchanged.ID != rejected.ID || unchanged.ContentSHA256 != rejected.ContentSHA256 {
				t.Fatalf("rename changed immutable bytes or version: found=%t err=%v", found, err)
			}
			content := "Existing files: [" + test.name + "]({{artifact:" + rejected.ID + "}})."
			if test.peer {
				content += " [peer.sdf]({{artifact:" + peer.ID + "}})."
			}
			model := &artifactReferenceRepairModel{content: content, testing: t}
			result, err := f.server.runSessionAgentWithArtifactReferenceRepair(context.Background(), session,
				agentruntime.Engine{Model: model}, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}}}, run, nil)
			if test.wantInvalid {
				var integrity *sessionRunnerReferenceIntegrityError
				if model.calls != 1 || !errors.As(err, &integrity) || len(integrity.InvalidScientificArtifacts) != 1 || integrity.InvalidScientificArtifacts[0] != "invalid_sdf_records" {
					t.Fatalf("unchanged compatible selection lost its proven set-level failure: final=%q err=%v", result.FinalMessage.Content, err)
				}
				return
			}
			if err != nil || model.calls != 1 || !strings.HasPrefix(result.FinalMessage.Content, content) ||
				strings.Count(result.FinalMessage.Content, "Earlier chemical-validation failures could not be matched") != 1 {
				t.Fatalf("renamed missing-format member bound its old failure to another artifact: final=%q err=%v", result.FinalMessage.Content, err)
			}
		})
	}
}
