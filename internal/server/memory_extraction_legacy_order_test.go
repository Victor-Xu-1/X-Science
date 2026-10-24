package server

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	eventjournal "x-science/internal/persistence/journal"
	workspace "x-science/internal/persistence/workspace"
)

func newLegacyCoverageRuntime(t *testing.T) (*Server, *memoryExtractionRuntime) {
	t.Helper()
	server := openWorkspaceMemoryToolServer(t)
	server.eventJournal = eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "journal"))
	server.memoryConfig.ExtractEnabled, server.memoryConfig.ExtractDelta = true, true
	server.memoryConfig.ExtractMode, server.memoryConfig.ExtractEveryN = "compact", 1
	server.memoryConfig.PIClassifierEnabled = false
	runtime := newMemoryExtractionRuntimeWithFactory(server, func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
			return map[string]any{}, nil
		})
	})
	return server, runtime
}

func appendLegacyCoverageCompletion(t *testing.T, server *Server, user, reply string, attempt int) int64 {
	t.Helper()
	var finishID int64
	for _, message := range []eventjournal.Message{
		{"type": "message", "role": "user", "text": user},
		{"type": "message", "role": "assistant", "text": reply},
		{"type": "runner_finished", "status": "completed", "runnerAttempt": attempt},
	} {
		entry, err := server.eventJournal.Append("frame-root", message, eventjournal.Metadata{})
		if err != nil {
			t.Fatal(err)
		}
		finishID = entry.EventID
	}
	return finishID
}

func captureLegacyCoverageRunInput(t *testing.T, ctx context.Context, runtime *memoryExtractionRuntime, finishID int64, delta bool) (memoryextract.RunInput, memoryExtractionSource) {
	t.Helper()
	resolved, err := runtime.resolveCompletedRootMemoryScope(ctx, "frame-root")
	if err != nil {
		t.Fatal(err)
	}
	source, err := runtime.captureCompletedMemorySource(ctx, "frame-root", finishID)
	if err != nil || source.proof != nil {
		t.Fatalf("capture actual legacy source: %+v %v", source, err)
	}
	scope := memoryextract.ApplyScope{UserID: resolved.Scope.UserID, ProjectID: resolved.Scope.ProjectID, SourceFrameID: resolved.Scope.SourceFrameID}
	snapshot, err := runtime.readMemoryExtractionSnapshot(ctx, scope.UserID, "frame-root", source)
	if err != nil || snapshot.TerminalStatus != "completed" {
		t.Fatal(snapshot.TerminalStatus, err)
	}
	bound, err := runtime.completedExtractionCommitSource(scope, source)
	if err != nil {
		t.Fatal(err)
	}
	config := memoryextract.ConfigFromMemoryConfig(runtime.server.memoryConfig)
	config.ExtractDelta = delta
	return memoryextract.RunInput{
		Scope: scope, Messages: snapshot.Messages, FinalResponseText: snapshot.FinalResponseText, Config: config,
		Source: &bound, CommitGuard: runtime.extractionCommitGuard(source, scope, len(snapshot.Messages)),
		CoveredPrefix: runtime.coveredMemoryPrefix("frame-root", source, snapshot, bound),
	}, source
}

func TestMemoryLegacyOlderSourceCannotCommitAfterLaterSameScalarReceipt(t *testing.T) {
	server, runtime := newLegacyCoverageRuntime(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	initialID := appendLegacyCoverageCompletion(t, server, "Original reporting convention.", "Original recorded.", 1)
	if result, err := runtime.RunCompletedRoot(ctx, "frame-root", initialID); err != nil || result.Gate != "done" || result.CursorAfter != 2 {
		t.Fatal(result, err)
	}
	// Replace the actual journal before starting either concurrent extraction.
	// The real scalar remains 2 from the original unselected journal receipt.
	server.eventJournal = eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "replacement-journal"))
	olderID := appendLegacyCoverageCompletion(t, server, "SELECTED_OLDER record confidence conventions.", "Selected older recorded.", 2)
	olderInput, olderSource := captureLegacyCoverageRunInput(t, ctx, runtime, olderID, true)
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	olderModel := runtime.sourceBoundMemoryExtractor(olderSource, memoryExtractorFunc(func(ctx context.Context, request memoryextract.ExtractRequest) (map[string]any, error) {
		if len(request.Delta) != 2 {
			t.Errorf("unproven replacement inherited scalar: delta=%d", len(request.Delta))
		}
		close(entered)
		select {
		case <-release:
			return map[string]any{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	olderRunner := memoryextract.NewRunner(server.workspaceStore, memoryextract.NewService(server.workspaceStore, nil, nil), olderModel)
	type outcome struct {
		result memoryextract.RunResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() { result, err := olderRunner.Run(ctx, olderInput); done <- outcome{result, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("older model did not reach its controlled barrier", ctx.Err())
	}
	newerID := appendLegacyCoverageCompletion(t, server, "SELECTED_NEWER record uncertainty.", "Selected newer recorded.", 3)
	newerInput, newerSource := captureLegacyCoverageRunInput(t, ctx, runtime, newerID, false)
	newerModel := runtime.sourceBoundMemoryExtractor(newerSource, memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
		return map[string]any{}, nil
	}))
	newerRunner := memoryextract.NewRunner(server.workspaceStore, memoryextract.NewService(server.workspaceStore, nil, nil), newerModel)
	newer, err := newerRunner.Run(ctx, newerInput)
	if err != nil || newer.Gate != "done" || newer.CursorBefore != 2 || newer.CursorAfter != 2 || newer.CursorAdvanced {
		t.Fatalf("actual later delta-disabled receipt did not preserve scalar: %+v %v", newer, err)
	}
	saved, found, err := server.workspaceStore.ReadMemoryExtractionReceipt(ctx, *newerInput.Source)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	unblock()
	select {
	case old := <-done:
		if !errors.Is(old.err, workspace.ErrExtractionSourceChanged) || old.result.Gate != "skipped-source" || old.result.CursorAdvanced {
			t.Fatalf("older same-scalar commit was not fenced: %+v %v", old.result, old.err)
		}
	case <-ctx.Done():
		t.Fatal("older extraction did not finish after barrier release", ctx.Err())
	}
	if _, found, err := server.workspaceStore.ReadMemoryExtractionReceipt(ctx, *olderInput.Source); err != nil || found {
		t.Fatalf("fenced older source committed a receipt: %t %v", found, err)
	}
	after, found, err := server.workspaceStore.ReadMemoryExtractionReceipt(ctx, *newerInput.Source)
	if err != nil || !found || !reflect.DeepEqual(saved, after) {
		t.Fatalf("late older write changed original later outcome: %t %v", found, err)
	}
	if cursor, err := server.workspaceStore.LastExtractMessageIndex(ctx, "frame-root"); err != nil || cursor != 2 {
		t.Fatalf("late older write changed physical cursor: %d %v", cursor, err)
	}
}

func TestMemoryLegacyRenumberedReplacementCanCommitAndRetainItsNewPrefix(t *testing.T) {
	server, runtime := newLegacyCoverageRuntime(t)
	var observed []memoryextract.ExtractRequest
	runtime.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(_ context.Context, request memoryextract.ExtractRequest) (map[string]any, error) {
			observed = append(observed, request)
			return map[string]any{}, nil
		})
	}
	firstID := appendLegacyCoverageCompletion(t, server, "Original first convention.", "ORIGINAL_FIRST retained.", 1)
	if result, err := runtime.RunCompletedRoot(t.Context(), "frame-root", firstID); err != nil || result.Gate != "done" {
		t.Fatal(result, err)
	}
	laterID := appendLegacyCoverageCompletion(t, server, "Original second convention.", "ORIGINAL_LATER retained.", 2)
	oldInput, _ := captureLegacyCoverageRunInput(t, t.Context(), runtime, laterID, true)
	if result, err := runtime.RunCompletedRoot(t.Context(), "frame-root", laterID); err != nil || result.Gate != "done" || result.CursorAfter != 4 {
		t.Fatal(result, err)
	}
	original, found, err := server.workspaceStore.ReadMemoryExtractionReceipt(t.Context(), *oldInput.Source)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	server.eventJournal = eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "renumbered-journal"))
	replacementID := appendLegacyCoverageCompletion(t, server, "REPLACEMENT_PROSE confidence intervals.", "REPLACEMENT_ANSWER recorded.", 3)
	replacement, err := runtime.RunCompletedRoot(t.Context(), "frame-root", replacementID)
	if err != nil || replacement.Gate != "done" || !replacement.Overshoot || replacement.CursorBefore != 4 || replacement.CursorAfter != 2 {
		t.Fatalf("unselected later old receipt blocked legitimate renumbering: %+v %v", replacement, err)
	}
	continuationID := appendLegacyCoverageCompletion(t, server, "NEW_CONTINUATION retain uncertainty.", "NEW_CONTINUATION_ANSWER recorded.", 4)
	continuation, err := runtime.RunCompletedRoot(t.Context(), "frame-root", continuationID)
	if err != nil || continuation.Gate != "done" || continuation.CursorBefore != 2 || continuation.CursorAfter != 4 ||
		len(observed) != 4 || len(observed[3].Delta) != 2 || !strings.Contains(observed[3].Transcript, "NEW_CONTINUATION") ||
		strings.Contains(observed[3].Transcript, "REPLACEMENT_ANSWER") || strings.Contains(observed[3].Transcript, "ORIGINAL_LATER") {
		t.Fatalf("replacement lineage did not retain exactly its new proven prefix: %+v calls=%d %v", continuation, len(observed), err)
	}
	after, found, err := server.workspaceStore.ReadMemoryExtractionReceipt(t.Context(), *oldInput.Source)
	if err != nil || !found || !reflect.DeepEqual(original, after) {
		t.Fatalf("replacement rewrote original legacy outcome: %t %v", found, err)
	}
}
