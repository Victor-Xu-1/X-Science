package memoryextract

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/memorytools"
	workspace "x-science/internal/persistence/workspace"
)

func TestWorkspaceExtractionRunnerReopensAndReplaysCommittedOutcome(t *testing.T) {
	ctx := t.Context()
	databasePath := filepath.Join(t.TempDir(), "workspace.db")
	store, err := workspace.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	prepareExtractionScope(t, store)
	if err := store.SetMemoryEnabled(ctx, "user", true); err != nil {
		t.Fatal(err)
	}
	modelCalls, classifierCalls := 0, 0
	service := NewService(store, extractionClassifierFunc(func(_ context.Context, texts []string) ([]memorytools.Classification, error) {
		classifierCalls++
		return make([]memorytools.Classification, len(texts)), nil
	}), nil)
	service.newID = func() string { return "mem_original_receipt" }
	runner := NewRunner(store, service, extractorFunc(func(context.Context, ExtractRequest) (map[string]any, error) {
		modelCalls++
		return map[string]any{"append": []any{map[string]any{
			"text": "Reproducible reports record their random seeds.", "evidence": "stated",
		}}}, nil
	}))
	input := bindExtractionSource(t, store, RunInput{
		Scope: ApplyScope{UserID: "user", ProjectID: "project", SourceFrameID: "frame"},
		Messages: []Message{{Role: "user", Content: []Block{{
			Type: BlockText, Text: "Remember to record random seeds in reproducible reports.",
		}}}},
		FinalResponseText: "The reporting preference is recorded.", Config: DefaultConfig(),
	})
	original, err := runner.Run(ctx, input)
	if err != nil || original.Gate != "done" || original.Replayed || !original.CursorAdvanced ||
		len(original.Applied.Appended) != 1 || original.Applied.Appended[0] != "mem_original_receipt" {
		t.Fatalf("original committed extraction = %#v, %v", original, err)
	}
	if modelCalls != 1 || classifierCalls != 1 {
		t.Fatalf("initial calls: model=%d classifier=%d", modelCalls, classifierCalls)
	}
	if err := store.SetMemoryEnabled(ctx, "user", false); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = workspace.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	// A replay is the original committed result, not a fresh grant, model call,
	// classifier decision, cadence counter or legacy journal write request.
	input.CommitGuard = nil
	input.Config.ExtractEveryN = 97
	service = NewService(store, extractionClassifierFunc(func(context.Context, []string) ([]memorytools.Classification, error) {
		classifierCalls++
		return nil, errors.New("replay must not classify again")
	}), nil)
	service.newID = func() string {
		t.Error("replay generated a new memory ID")
		return "mem_must_not_exist"
	}
	runner = NewRunner(store, service, extractorFunc(func(context.Context, ExtractRequest) (map[string]any, error) {
		modelCalls++
		return nil, errors.New("replay must not call the model again")
	}))
	replayed, err := runner.Run(ctx, input)
	if err != nil || replayed.Gate != original.Gate || !replayed.Replayed ||
		replayed.CursorBefore != original.CursorBefore || replayed.CursorAfter != original.CursorAfter ||
		replayed.CursorAdvanced != original.CursorAdvanced || replayed.Overshoot != original.Overshoot ||
		replayed.DeltaCount != original.DeltaCount || !reflect.DeepEqual(replayed.Applied, original.Applied) {
		t.Fatalf("replayed extraction = %#v, original=%#v, err=%v", replayed, original, err)
	}
	if modelCalls != 1 || classifierCalls != 1 {
		t.Fatalf("replay calls: model=%d classifier=%d", modelCalls, classifierCalls)
	}
	if count, err := store.CountMemoriesForUser(ctx, "user"); err != nil || count != 1 {
		t.Fatalf("replayed memory rows = %d, err=%v", count, err)
	}
	if cursor, err := store.LastExtractMessageIndex(ctx, "frame"); err != nil || cursor != 1 {
		t.Fatalf("replayed cursor = %d, err=%v", cursor, err)
	}
}

func TestWorkspaceExtractionPrepareDoesNotHoldWriteLockWhileClassifying(t *testing.T) {
	store := openExtractionStore(t)
	prepareExtractionScope(t, store)
	entered, release := make(chan struct{}), make(chan struct{})
	releaseClassifier := sync.OnceFunc(func() { close(release) })
	defer releaseClassifier()
	service := NewService(store, extractionClassifierFunc(func(ctx context.Context, texts []string) ([]memorytools.Classification, error) {
		close(entered)
		select {
		case <-release:
			return make([]memorytools.Classification, len(texts)), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}), nil)
	type preparedResult struct {
		plan workspace.ExtractionMutationPlan
		err  error
	}
	prepared := make(chan preparedResult, 1)
	go func() {
		plan, err := service.Prepare(t.Context(), ApplyScope{UserID: "user", ProjectID: "project", SourceFrameID: "frame"}, Operations{
			Append: []AppendOperation{{Text: "Reports preserve reproducibility settings.", Evidence: "observed"}},
		})
		prepared <- preparedResult{plan: plan, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("classification did not start")
	}
	writeCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := store.WithTransaction(writeCtx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(writeCtx, `UPDATE projects SET name = 'Changed while classifying' WHERE id = 'project'`)
		return err
	}); err != nil {
		t.Fatalf("classification held a SQLite write lock: %v", err)
	}
	if count, err := store.CountMemoriesForUser(t.Context(), "user"); err != nil || count != 0 {
		t.Fatalf("classification wait persisted %d memory rows, err=%v", count, err)
	}
	releaseClassifier()
	select {
	case result := <-prepared:
		if result.err != nil || len(result.plan.Append) != 1 || len(result.plan.Prepared.Appended) != 0 {
			t.Fatalf("prepared extraction = %#v, %v", result.plan, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("classification did not finish after release")
	}
	if count, err := store.CountMemoriesForUser(t.Context(), "user"); err != nil || count != 0 {
		t.Fatalf("prepare without commit persisted %d memory rows, err=%v", count, err)
	}
}

func TestWorkspaceExtractionPrepareLateClassifierErrorHasNoEffects(t *testing.T) {
	store := openExtractionStore(t)
	prepareExtractionScope(t, store)
	ctx := t.Context()
	createExtractionMemory(t, store, workspace.CreateMemoryInput{
		ID: "mem_predecessor", UserID: "user", Body: "The reporting format is stable.", Origin: "extractor",
		Evidence: "observed", SubjectProjectID: "project", SourceFrameID: "frame",
	})
	classifierErr := errors.New("replacement classifier failed after append preparation")
	calls := 0
	service := NewService(store, extractionClassifierFunc(func(_ context.Context, texts []string) ([]memorytools.Classification, error) {
		calls++
		if calls == 2 {
			return nil, classifierErr
		}
		return make([]memorytools.Classification, len(texts)), nil
	}), nil)
	service.newID = func() string { return "mem_pending_append" }
	plan, err := service.Prepare(ctx, ApplyScope{UserID: "user", ProjectID: "project", SourceFrameID: "frame"}, Operations{
		Append:  []AppendOperation{{Text: "The reproducibility policy is durable.", Evidence: "stated"}},
		Replace: []ReplaceOperation{{ID: "mem_predecessor", Text: "The reporting format has been updated.", Evidence: "observed"}},
	})
	if !errors.Is(err, classifierErr) || calls != 2 || len(plan.Append) != 1 || len(plan.Prepared.Appended) != 0 {
		t.Fatalf("late prepare error = %#v, calls=%d, err=%v", plan, calls, err)
	}
	if _, found, err := store.GetMemoryOwned(ctx, "user", "mem_pending_append"); err != nil || found {
		t.Fatalf("an early append survived a late preparation failure: found=%v, err=%v", found, err)
	}
	active, found, err := store.ResolveActiveMemoryOwned(ctx, "user", "mem_predecessor")
	if err != nil || !found || active.ID != "mem_predecessor" || active.Body != "The reporting format is stable." {
		t.Fatalf("late prepare failure changed predecessor = %#v, found=%v, err=%v", active, found, err)
	}
	if count, err := store.CountMemoriesForUser(ctx, "user"); err != nil || count != 1 {
		t.Fatalf("late prepare failure persisted %d rows, err=%v", count, err)
	}
}

func TestWorkspaceExtractionRunnerRejectsChangedReplayInput(t *testing.T) {
	store := openExtractionStore(t)
	prepareExtractionScope(t, store)
	ctx := t.Context()
	if err := store.SetMemoryEnabled(ctx, "user", true); err != nil {
		t.Fatal(err)
	}
	modelCalls := 0
	service := NewService(store, extractionClassifierFunc(func(_ context.Context, texts []string) ([]memorytools.Classification, error) {
		return make([]memorytools.Classification, len(texts)), nil
	}), nil)
	service.newID = func() string { return "mem_unmodified_receipt" }
	runner := NewRunner(store, service, extractorFunc(func(context.Context, ExtractRequest) (map[string]any, error) {
		modelCalls++
		return map[string]any{"append": []any{map[string]any{
			"text": "Record explicit seeds for reproducible analysis.", "evidence": "stated",
		}}}, nil
	}))
	input := bindExtractionSource(t, store, RunInput{
		Scope: ApplyScope{UserID: "user", ProjectID: "project", SourceFrameID: "frame"},
		Messages: []Message{{Role: "user", Content: []Block{{
			Type: BlockText, Text: "Record explicit seeds when running reproducible analysis.",
		}}}},
		FinalResponseText: "The original reporting preference is recorded.", Config: DefaultConfig(),
	})
	original, err := runner.Run(ctx, input)
	if err != nil || original.Gate != "done" || len(original.Applied.Appended) != 1 {
		t.Fatalf("initial extraction = %#v, %v", original, err)
	}
	for _, test := range []struct {
		name   string
		change func(*RunInput)
	}{
		{name: "messages", change: func(changed *RunInput) {
			changed.Messages[0].Content[0].Text = "This is a different immutable completed input."
		}},
		{name: "final-response", change: func(changed *RunInput) {
			changed.FinalResponseText = "This is a different completed final response."
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := input
			changed.Messages = append([]Message(nil), input.Messages...)
			changed.Messages[0].Content = append([]Block(nil), input.Messages[0].Content...)
			test.change(&changed)
			result, err := runner.Run(ctx, changed)
			if !errors.Is(err, workspace.ErrExtractionPlanConflict) || result.Gate != "replay-conflict" ||
				result.CursorAdvanced || len(result.Applied.Appended) != 0 || modelCalls != 1 {
				t.Fatalf("changed replay = %#v, model calls=%d, err=%v", result, modelCalls, err)
			}
			if count, err := store.CountMemoriesForUser(ctx, "user"); err != nil || count != 1 {
				t.Fatalf("changed replay memory rows = %d, err=%v", count, err)
			}
			if cursor, err := store.LastExtractMessageIndex(ctx, "frame"); err != nil || cursor != original.CursorAfter {
				t.Fatalf("changed replay cursor = %d, err=%v", cursor, err)
			}
		})
	}
	replayed, err := runner.Run(ctx, input)
	if err != nil || !replayed.Replayed || !reflect.DeepEqual(replayed.Applied, original.Applied) || modelCalls != 1 {
		t.Fatalf("input conflicts changed the original receipt: %#v, model calls=%d, err=%v", replayed, modelCalls, err)
	}
}

func TestWorkspaceExtractionRunnerRejectsUnfencedFreshSource(t *testing.T) {
	for _, test := range []string{"missing-source", "legacy-without-guard"} {
		t.Run(test, func(t *testing.T) {
			store := openExtractionStore(t)
			prepareExtractionScope(t, store)
			ctx := t.Context()
			if err := store.SetMemoryEnabled(ctx, "user", true); err != nil {
				t.Fatal(err)
			}
			service := NewService(store, extractionClassifierFunc(func(_ context.Context, texts []string) ([]memorytools.Classification, error) {
				return make([]memorytools.Classification, len(texts)), nil
			}), nil)
			service.newID = func() string { return "mem_must_not_commit" }
			runner := NewRunner(store, service, extractorFunc(func(context.Context, ExtractRequest) (map[string]any, error) {
				return map[string]any{"append": []any{map[string]any{
					"text": "An unfenced input must never become durable memory.", "evidence": "observed",
				}}}, nil
			}))
			input := bindExtractionSource(t, store, RunInput{
				Scope: ApplyScope{UserID: "user", ProjectID: "project", SourceFrameID: "frame"},
				Messages: []Message{{Role: "user", Content: []Block{{
					Type: BlockText, Text: "Remember this reporting preference after the completed turn.",
				}}}},
				Config: DefaultConfig(),
			})
			if test == "missing-source" {
				input.Source = nil
			}
			input.CommitGuard = nil
			result, err := runner.Run(ctx, input)
			if !errors.Is(err, workspace.ErrExtractionSourceChanged) || result.Gate != "skipped-source" ||
				result.CursorAdvanced || result.CursorAfter != 0 || len(result.Applied.Appended) != 0 {
				t.Fatalf("unfenced fresh extraction = %#v, %v", result, err)
			}
			if count, err := store.CountMemoriesForUser(ctx, "user"); err != nil || count != 0 {
				t.Fatalf("unfenced extraction persisted %d rows, err=%v", count, err)
			}
			if cursor, err := store.LastExtractMessageIndex(ctx, "frame"); err != nil || cursor != 0 {
				t.Fatalf("unfenced extraction advanced cursor to %d, err=%v", cursor, err)
			}
		})
	}
}

func TestWorkspaceExtractionRunnerConcurrentPlansReplayOneWinningReceipt(t *testing.T) {
	store := openExtractionStore(t)
	prepareExtractionScope(t, store)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := store.SetMemoryEnabled(ctx, "user", true); err != nil {
		t.Fatal(err)
	}
	input := bindExtractionSource(t, store, RunInput{
		Scope: ApplyScope{UserID: "user", ProjectID: "project", SourceFrameID: "frame"},
		Messages: []Message{{Role: "user", Content: []Block{{
			Type: BlockText, Text: "Preserve reproducibility settings from this completed report.",
		}}}},
		FinalResponseText: "The report records explicit reproducibility settings.", Config: DefaultConfig(),
	})
	strictGuard := input.CommitGuard
	classified, prepared := make(chan string, 2), make(chan string, 2)
	releaseClassification, releaseCommit := make(chan struct{}), make(chan struct{})
	allowClassification := sync.OnceFunc(func() { close(releaseClassification) })
	allowCommit := sync.OnceFunc(func() { close(releaseCommit) })
	defer allowClassification()
	defer allowCommit()
	var modelCalls, classifierCalls, generatedIDs atomic.Int32
	type runnerOutcome struct {
		result RunResult
		err    error
	}
	outcomes := make(chan runnerOutcome, 2)
	bodies := map[string]string{
		"mem_plan_left":  "Reports preserve explicit random seeds for reproducibility.",
		"mem_plan_right": "Analysis reports record reproducibility settings and random seeds.",
	}
	for _, id := range []string{"mem_plan_left", "mem_plan_right"} {
		service := NewService(store, extractionClassifierFunc(func(ctx context.Context, texts []string) ([]memorytools.Classification, error) {
			classifierCalls.Add(1)
			classified <- id
			select {
			case <-releaseClassification:
				return make([]memorytools.Classification, len(texts)), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}), nil)
		service.newID = func() string {
			generatedIDs.Add(1)
			return id
		}
		runner := NewRunner(store, service, extractorFunc(func(context.Context, ExtractRequest) (map[string]any, error) {
			modelCalls.Add(1)
			return map[string]any{"append": []any{map[string]any{"text": bodies[id], "evidence": "observed"}}}, nil
		}))
		captured := input
		captured.CommitGuard = func(ctx context.Context, commit func() error) error {
			// Preparation, including distinct IDs and accepted bodies, is finished
			// before either runner may enter the real strict journal/SQL guard.
			prepared <- id
			select {
			case <-releaseCommit:
				return strictGuard(ctx, commit)
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		go func() {
			result, err := runner.Run(ctx, captured)
			outcomes <- runnerOutcome{result: result, err: err}
		}()
	}
	waitForBoth := func(stage string, arrivals <-chan string) {
		t.Helper()
		seen := make(map[string]bool, 2)
		for len(seen) < 2 {
			select {
			case id := <-arrivals:
				if seen[id] {
					t.Fatalf("runner entered %s twice: %s", stage, id)
				}
				seen[id] = true
			case <-ctx.Done():
				t.Fatalf("both runners did not reach %s: %v", stage, ctx.Err())
			}
		}
	}
	waitForBoth("classification", classified)
	allowClassification()
	waitForBoth("prepared commit", prepared)
	if generatedIDs.Load() != 2 {
		t.Fatalf("expected distinct prepared IDs before either commit, got %d", generatedIDs.Load())
	}
	allowCommit()
	results := make([]RunResult, 0, 2)
	for len(results) < 2 {
		select {
		case outcome := <-outcomes:
			if outcome.err != nil || outcome.result.Gate != "done" || len(outcome.result.Applied.Appended) != 1 {
				t.Fatalf("concurrent runner result = %#v, %v", outcome.result, outcome.err)
			}
			results = append(results, outcome.result)
		case <-ctx.Done():
			t.Fatalf("concurrent extraction did not finish: %v", ctx.Err())
		}
	}
	if results[0].Replayed == results[1].Replayed || !reflect.DeepEqual(results[0].Applied, results[1].Applied) ||
		results[0].CursorBefore != 0 || results[1].CursorBefore != 0 ||
		results[0].CursorAfter != 1 || results[1].CursorAfter != 1 ||
		!results[0].CursorAdvanced || !results[1].CursorAdvanced {
		t.Fatalf("both runners must return one original winning outcome: %#v", results)
	}
	if modelCalls.Load() != 2 || classifierCalls.Load() != 2 || generatedIDs.Load() != 2 {
		t.Fatalf("concurrent replay reran preparation: model=%d classifier=%d IDs=%d", modelCalls.Load(), classifierCalls.Load(), generatedIDs.Load())
	}
	winnerID := results[0].Applied.Appended[0]
	memory, found, err := store.GetMemoryOwned(ctx, "user", winnerID)
	if err != nil || !found || memory.Body != bodies[winnerID] {
		t.Fatalf("winning persisted fact = %#v, found=%v, err=%v", memory, found, err)
	}
	if count, err := store.CountMemoriesForUser(ctx, "user"); err != nil || count != 1 {
		t.Fatalf("concurrent extraction persisted %d facts, err=%v", count, err)
	}
	if cursor, err := store.LastExtractMessageIndex(ctx, "frame"); err != nil || cursor != 1 {
		t.Fatalf("concurrent extraction cursor = %d, err=%v", cursor, err)
	}
	var receipts int
	if err := store.WithTransaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_extraction_receipts`).Scan(&receipts)
	}); err != nil || receipts != 1 {
		t.Fatalf("concurrent extraction receipts = %d, err=%v", receipts, err)
	}
}
