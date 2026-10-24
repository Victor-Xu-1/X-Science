package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	"x-science/internal/outbox"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestMemoryDurableEqualLengthReplacementBranchDoesNotBorrowCursor(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	f.server.memoryConfig.ExtractDelta = true
	f.server.memoryConfig.ExtractMode = "forked"
	var observed []string
	f.server.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(_ context.Context, request memoryextract.ExtractRequest) (map[string]any, error) {
			observed = append(observed, request.Transcript)
			return map[string]any{}, nil // A genuine empty outcome, not malformed output.
		})
	}
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{
		Messages: []agentruntime.Message{{Role: "user", Content: "BRANCH_A_VIEW preserve reporting conventions."}},
	})
	firstResult, err := settleDurableMemoryFixture(t, f, "BRANCH_A_ANSWER recorded the original convention.", nil)
	if err != nil || firstResult.Status != "completed" {
		t.Fatalf("first task completion=%+v err=%v", firstResult, err)
	}
	first := claimDurableMemoryFixture(t, f.store, "branch-cursor-first")
	firstValue, err := workspace.DecodeMemoryExtractionDispatch(first)
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot, err := f.server.memoryExtraction.readMemoryExtractionSnapshot(t.Context(), f.stream.OwnerID,
		f.stream.SessionID, memoryExtractionSource{proof: firstValue.Source.Transcript, finishEventID: firstResult.FinishEventID, captured: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.deliverMemoryExtraction(t.Context(), first); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatal(err)
	}
	firstReceipt, found, err := f.store.ReadMemoryExtractionReceipt(t.Context(), firstValue.Source)
	if err != nil || !found || len(observed) != 1 || firstReceipt.Gate != "done" ||
		firstReceipt.CursorAfter <= 0 || firstReceipt.CursorAfter != len(firstSnapshot.Messages) {
		t.Fatalf("first completion must really commit its cursor: receipt=%+v found=%t calls=%d err=%v", firstReceipt, found, len(observed), err)
	}
	var firstRaw string
	if err := f.db.QueryRowContext(t.Context(), `SELECT result_json FROM memory_extraction_receipts WHERE source_key=?`, first.IdempotencyKey).Scan(&firstRaw); err != nil {
		t.Fatal(err)
	}
	branch, err := f.repo.GetBranchState(t.Context(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ForkFrameUserMessageBranch(t.Context(), transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: branch.ActiveBranchID,
		ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation,
		ClientMutationID: "memory-cursor-replacement", SourceClientMessageID: "save-user", SourceMessageIndex: 0,
		ReplacementText: "BRANCH_B_ONLY retain explicit confidence intervals in future reports.", Destinations: []string{"ws"},
	}); err != nil {
		t.Fatal(err)
	}
	f.stream, err = f.repo.GetStream(t.Context(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	claimSkillControlInput(t, f)
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{
		Messages: []agentruntime.Message{{Role: "user", Content: "BRANCH_B_VIEW retain explicit confidence intervals."}},
	})
	secondResult, err := settleDurableMemoryFixture(t, f, "BRANCH_B_ANSWER recorded the corrected convention.", nil)
	if err != nil || secondResult.Status != "completed" {
		t.Fatalf("replacement task completion=%+v err=%v", secondResult, err)
	}
	second := claimDurableMemoryFixture(t, f.store, "branch-cursor-second")
	secondValue, err := workspace.DecodeMemoryExtractionDispatch(second)
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot, err := f.server.memoryExtraction.readMemoryExtractionSnapshot(t.Context(), f.stream.OwnerID,
		f.stream.SessionID, memoryExtractionSource{proof: secondValue.Source.Transcript, finishEventID: secondResult.FinishEventID, captured: true})
	if err != nil || len(secondSnapshot.Messages) != len(firstSnapshot.Messages) ||
		secondValue.Source.Transcript.BranchID == firstValue.Source.Transcript.BranchID || second.IdempotencyKey == first.IdempotencyKey {
		t.Fatalf("fixture must contain a distinct equal-length selected completion: first=%d second=%d err=%v", len(firstSnapshot.Messages), len(secondSnapshot.Messages), err)
	}
	if _, found, err := f.store.ReadMemoryExtractionReceipt(t.Context(), secondValue.Source); err != nil || found {
		t.Fatalf("replacement source unexpectedly already committed: found=%t err=%v", found, err)
	}
	deliveryErr := f.server.deliverMemoryExtraction(t.Context(), second)
	if !errors.Is(deliveryErr, outbox.ErrDeliverySettled) {
		t.Errorf("replacement delivery did not settle its own outcome: %v", deliveryErr)
	}
	if len(observed) != 2 {
		t.Errorf("distinct equal-length branch borrowed the old cursor: extractor calls=%d want=2", len(observed))
	} else if !strings.Contains(observed[1], "BRANCH_B_ONLY") || strings.Contains(observed[1], "BRANCH_A_ANSWER") {
		t.Errorf("replacement extractor did not receive only its selected completed history: %q", observed[1])
	}
	secondReceipt, found, err := f.store.ReadMemoryExtractionReceipt(t.Context(), secondValue.Source)
	if err != nil || !found || secondReceipt.Gate != "done" || secondReceipt.InputSHA256 == firstReceipt.InputSHA256 ||
		secondReceipt.CursorAfter != len(secondSnapshot.Messages) {
		t.Errorf("replacement was acknowledged without its own source receipt: receipt=%+v found=%t err=%v", secondReceipt, found, err)
	}
	saved, err := f.store.GetOutboxEvent(t.Context(), second.ID)
	if err != nil || saved.Status != workspace.OutboxStatusDelivered || saved.LastError != "" {
		t.Errorf("replacement transport settlement=%+v err=%v", saved, err)
	}
	var retainedRaw, status string
	if err := f.db.QueryRowContext(t.Context(), `SELECT result_json FROM memory_extraction_receipts WHERE source_key=?`, first.IdempotencyKey).Scan(&retainedRaw); err != nil || retainedRaw != firstRaw {
		t.Errorf("replacement changed the original immutable receipt: %v", err)
	}
	if err := f.db.QueryRowContext(t.Context(), `SELECT status FROM frames WHERE id=?`, f.stream.FrameID).Scan(&status); err != nil || status != "completed" {
		t.Errorf("optional extraction changed the completed user task: status=%s err=%v", status, err)
	}
}
