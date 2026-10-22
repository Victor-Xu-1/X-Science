package server

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	settingsstore "x-science/internal/persistence/settings"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

type atomicExtractionClassifier func(context.Context, []string) ([]memorytools.Classification, error)

func (classify atomicExtractionClassifier) ClassifyExtractionWrites(ctx context.Context, texts []string) ([]memorytools.Classification, error) {
	return classify(ctx, texts)
}

type atomicExtractionRepairer func(context.Context, string, string) (string, error)

func (repair atomicExtractionRepairer) RepairMemoryReplacement(ctx context.Context, before, after string) (string, error) {
	return repair(ctx, before, after)
}

func TestMemoryExtractionBranchRevokedDuringPreparationCannotCommit(t *testing.T) {
	for _, stage := range []string{"classifier", "literal repair", "automatic opt-out"} {
		t.Run(stage, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			ctx := t.Context()
			f.server.settingsStore = settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
			f.server.memoryConfig.ExtractEnabled = true
			if err := f.store.SetMemoryEnabled(ctx, f.stream.OwnerID, true); err != nil {
				t.Fatal(err)
			}
			finish, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
				Claim: f.claim, ClientMessageID: "prepare-fence-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			proof, err := f.repo.ReadCompletedMemorySource(ctx, f.stream.UID, f.stream.OwnerID, finish.EventID)
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			releasePreparation := sync.OnceFunc(func() { close(release) })
			defer releasePreparation()
			wait := func(ctx context.Context) error {
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			classifier := atomicExtractionClassifier(func(ctx context.Context, texts []string) ([]memorytools.Classification, error) {
				if stage != "literal repair" {
					if err := wait(ctx); err != nil {
						return nil, err
					}
				}
				return make([]memorytools.Classification, len(texts)), nil
			})
			repairer := atomicExtractionRepairer(func(ctx context.Context, _, _ string) (string, error) {
				if err := wait(ctx); err != nil {
					return "", err
				}
				return "Use v4.0.3 (was v4.0.2) at /opt/report.json", nil
			})
			initialCount := 0
			emitted := map[string]any{"append": []any{map[string]any{"text": "Reports record explicit random seeds.", "evidence": "stated"}}}
			if stage == "literal repair" {
				if _, err := f.store.CreateMemory(workspace.CreateMemoryInput{
					ID: "mem_repair_predecessor", UserID: f.stream.OwnerID, Origin: "extractor", Evidence: "stated",
					Body: "Use v4.0.2 at /opt/report.json", SubjectProjectID: f.stream.ProjectID, SourceFrameID: f.stream.FrameID,
				}); err != nil {
					t.Fatal(err)
				}
				initialCount = 1
				emitted = map[string]any{"replace": []any{map[string]any{"id": "mem_repair_predecessor", "text": "Use v4.0.3", "evidence": "observed"}}}
			}
			service := memoryextract.NewService(f.store, classifier, repairer)
			model := memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) { return emitted, nil })
			runner := memoryextract.NewRunner(f.store, service, model)
			config := memoryextract.DefaultConfig()
			config.Enabled, config.ExtractEnabled, config.ExtractMode = true, true, "compact"
			scope := memoryextract.ApplyScope{UserID: f.stream.OwnerID, ProjectID: f.stream.ProjectID, SourceFrameID: f.stream.FrameID}
			runtime := newMemoryExtractionRuntimeWithFactory(f.server, nil)
			input := memoryextract.RunInput{
				Scope: scope,
				Source: &workspace.CompletedExtractionSource{Schema: 1, OwnerUserID: f.stream.OwnerID,
					ProjectID: f.stream.ProjectID, FrameID: f.stream.FrameID, Transcript: &proof},
				Messages: []memoryextract.Message{{Role: "user", Content: []memoryextract.Block{{Type: memoryextract.BlockText, Text: "Preserve this durable reporting preference."}}}},
				Config:   config,
			}
			input.CommitGuard = runtime.extractionCommitGuard(memoryExtractionSource{captured: true, proof: &proof, finishEventID: finish.EventID}, scope, len(input.Messages))
			type response struct {
				result memoryextract.RunResult
				err    error
			}
			done := make(chan response, 1)
			go func() {
				result, err := runner.Run(ctx, input)
				done <- response{result, err}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("preparation did not reach its controlled wait")
			}
			wantError, wantGate := workspace.ErrExtractionSourceChanged, "skipped-source"
			if stage == "automatic opt-out" {
				wantError, wantGate = workspace.ErrAgentMemoryMutationForbidden, "commit-failed"
				if err := f.server.setMemoryAutoExtractionEnabled(f.stream.OwnerID, false); err != nil {
					t.Fatal(err)
				}
			} else {
				branch, err := f.repo.GetBranchState(ctx, f.stream.UID, f.stream.OwnerID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.repo.ForkFrameUserMessageBranch(ctx, transcriptstore.ForkFrameUserMessageBranchInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: branch.ActiveBranchID,
					ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation,
					ClientMutationID: "prepare-fence-fork", SourceClientMessageID: "save-user", SourceMessageIndex: 0,
					ReplacementText: "The user selected a replacement branch.", Destinations: []string{"ws"},
				}); err != nil {
					t.Fatal(err)
				}
			}
			releasePreparation()
			select {
			case completed := <-done:
				if !errors.Is(completed.err, wantError) || completed.result.Gate != wantGate ||
					len(completed.result.Applied.Appended)+len(completed.result.Applied.Replaced) != 0 {
					t.Fatalf("revoked preparation result=%#v err=%v", completed.result, completed.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("revoked extraction did not finish after release")
			}
			if count, err := f.store.CountMemoriesForUser(ctx, f.stream.OwnerID); err != nil || count != initialCount {
				t.Fatalf("revoked preparation changed facts: count=%d err=%v", count, err)
			}
			if cursor, err := f.store.LastExtractMessageIndex(ctx, f.stream.FrameID); err != nil || cursor != 0 {
				t.Fatalf("revoked preparation advanced cursor=%d err=%v", cursor, err)
			}
			if terminal, err := f.repo.GetTerminalProjection(ctx, f.stream.OwnerID, f.stream.UID, finish.EventID); err != nil || terminal.TerminalStatus != "completed" {
				t.Fatalf("optional extraction changed completed task: %#v err=%v", terminal, err)
			}
		})
	}
}
