package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	"x-science/internal/outbox"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestMemoryDurableReplacementRetainsProvenCompletedPrefix(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	f.server.memoryConfig.ExtractDelta = true
	var observed []memoryextract.ExtractRequest
	f.server.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(_ context.Context, request memoryextract.ExtractRequest) (map[string]any, error) {
			observed = append(observed, request)
			return map[string]any{}, nil
		})
	}
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "FIRST_VIEW retained conventions"}}})
	firstResult, err := settleDurableMemoryFixture(t, f, "RETAINED_PREFIX_ANSWER preserve reproducibility conventions.", nil)
	if err != nil || firstResult.Status != "completed" {
		t.Fatal(firstResult, err)
	}
	first := claimDurableMemoryFixture(t, f.store, "coverage-first")
	firstValue, err := workspace.DecodeMemoryExtractionDispatch(first)
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot, err := f.server.memoryExtraction.readMemoryExtractionSnapshot(t.Context(), f.stream.OwnerID, f.stream.SessionID,
		memoryExtractionSource{proof: firstValue.Source.Transcript, finishEventID: firstResult.FinishEventID, captured: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.deliverMemoryExtraction(t.Context(), first); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatal(err)
	}
	if len(observed) != 1 || len(firstSnapshot.Messages) != 2 {
		t.Fatalf("first real extraction/snapshot: calls=%d messages=%d", len(observed), len(firstSnapshot.Messages))
	}
	var original string
	if err := f.db.QueryRow(`SELECT result_json FROM memory_extraction_receipts WHERE source_key=?`, first.IdempotencyKey).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "coverage-pending-user",
		FrameEventID: "coverage-pending-event", MessageUUID: "coverage-pending-message", Text: "PENDING_REPLACED_TEXT update reporting conventions.",
	}); err != nil {
		t.Fatal(err)
	}
	branch, err := f.repo.GetBranchState(t.Context(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ForkFrameUserMessageBranch(t.Context(), transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: branch.ActiveBranchID,
		ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation,
		ClientMutationID: "coverage-retained-fork", SourceClientMessageID: "coverage-pending-user",
		SourceMessageIndex: int64(len(firstSnapshot.Messages)), ReplacementText: "NEW_SELECTED_PROSE preserve explicit confidence intervals.", Destinations: []string{"ws"},
	}); err != nil {
		t.Fatal(err)
	}
	f.stream, err = f.repo.GetStream(t.Context(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	claimSkillControlInput(t, f)
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "NEW_VIEW confidence intervals"}}})
	result, err := settleDurableMemoryFixture(t, f, "NEW_SELECTED_ANSWER recorded the confidence convention.", nil)
	if err != nil || result.Status != "completed" {
		t.Fatal(result, err)
	}
	second := claimDurableMemoryFixture(t, f.store, "coverage-second")
	if err := f.server.deliverMemoryExtraction(t.Context(), second); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatal(err)
	}
	if len(observed) != 2 {
		t.Fatalf("new selected source was not processed: calls=%d", len(observed))
	}
	if len(observed[1].Messages) != 4 || len(observed[1].Delta) != 2 ||
		!strings.Contains(observed[1].Transcript, "NEW_SELECTED_PROSE") || strings.Contains(observed[1].Transcript, "RETAINED_PREFIX_ANSWER") ||
		strings.Contains(observed[1].Transcript, "PENDING_REPLACED_TEXT") {
		t.Fatalf("did not preserve exactly the proven selected prefix: %+v", observed[1])
	}
	var retained string
	if err := f.db.QueryRow(`SELECT result_json FROM memory_extraction_receipts WHERE source_key=?`, first.IdempotencyKey).Scan(&retained); err != nil || retained != original {
		t.Fatalf("new branch rewrote original outcome: %v", err)
	}
}

func TestMemoryLegacyCoverageRetainsOnlyStrictCompletedPrefix(t *testing.T) {
	server := openWorkspaceMemoryToolServer(t)
	server.eventJournal = eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "journal"))
	server.memoryConfig.ExtractEnabled = true
	server.memoryConfig.ExtractDelta = true
	server.memoryConfig.ExtractMode = "compact"
	server.memoryConfig.ExtractEveryN = 1
	server.memoryConfig.PIClassifierEnabled = false
	var observed []memoryextract.ExtractRequest
	runtime := newMemoryExtractionRuntimeWithFactory(server, func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(_ context.Context, request memoryextract.ExtractRequest) (map[string]any, error) {
			observed = append(observed, request)
			return map[string]any{}, nil
		})
	})
	complete := func(user, reply string, attempt int) int64 {
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
	firstID := complete("Retain original reproducibility preferences.", "LEGACY_FIRST_ANSWER retained the original preference.", 1)
	first, err := runtime.RunCompletedRoot(t.Context(), "frame-root", firstID)
	if err != nil || first.Gate != "done" || first.CursorAfter != 2 {
		t.Fatalf("first actual legacy outcome=%+v err=%v", first, err)
	}
	secondID := complete("LEGACY_NEW_PROSE retain explicit confidence intervals.", "LEGACY_NEW_ANSWER recorded the confidence preference.", 2)
	second, err := runtime.RunCompletedRoot(t.Context(), "frame-root", secondID)
	if err != nil || second.Gate != "done" || second.CursorBefore != 2 || second.CursorAfter != 4 || len(observed) != 2 {
		t.Fatalf("second actual legacy outcome=%+v calls=%d err=%v", second, len(observed), err)
	}
	if len(observed[1].Delta) != 2 || !strings.Contains(observed[1].Transcript, "LEGACY_NEW_PROSE") || strings.Contains(observed[1].Transcript, "LEGACY_FIRST_ANSWER") {
		t.Fatalf("legacy did not preserve its strict completed prefix: %+v", observed[1])
	}
	again, err := runtime.RunCompletedRoot(t.Context(), "frame-root", firstID)
	if err != nil || !again.Replayed || again.CursorAfter != first.CursorAfter || len(observed) != 2 {
		t.Fatalf("old legacy receipt was regenerated: result=%+v calls=%d err=%v", again, len(observed), err)
	}
}
