package server

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"x-science/internal/memoryextract"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func (runtime *memoryExtractionRuntime) coveredMemoryPrefix(sessionID string, source memoryExtractionSource, current memoryExtractionJournalSnapshot, bound workspace.CompletedExtractionSource) func(context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		legacy, err := runtime.validateMemoryCoverageSource(ctx, source)
		if err != nil {
			return 0, err
		}
		candidates, err := runtime.server.workspaceStore.ReadMemoryExtractionCoverageReceipts(ctx, bound)
		if err != nil {
			return 0, err
		}
		covered := 0
		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			prior, usable, err := runtime.readMemoryCoverageSnapshot(ctx, sessionID, source, candidate.Source, legacy)
			if err != nil {
				return 0, err
			}
			if !usable {
				continue
			}
			if prior.TerminalStatus != "completed" {
				return 0, workspace.ErrExtractionPlanConflict
			}
			covered, err = memoryextract.AdvanceExtractionCoverage(current.Messages, covered, prior.Messages, prior.FinalResponseText, candidate.Result)
			if err != nil {
				return 0, err
			}
		}
		if _, err := runtime.validateMemoryCoverageSource(ctx, source); err != nil {
			return 0, err
		}
		return covered, nil
	}
}

// The original history readers remain authoritative. Unlike a valid missing
// candidate, I/O/cancellation errors cannot turn into successful empty coverage.
func (runtime *memoryExtractionRuntime) validateMemoryCoverageSource(ctx context.Context, source memoryExtractionSource) ([]eventjournal.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.proof != nil {
		actual, err := runtime.server.transcriptStore.ReadCompletedMemorySource(ctx, source.proof.StreamUID, source.proof.OwnerID, source.finishEventID)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, transcriptstore.ErrEventConflict) {
			return nil, workspace.ErrExtractionSourceChanged
		}
		if err != nil {
			return nil, err
		}
		if actual != *source.proof {
			return nil, workspace.ErrExtractionSourceChanged
		}
		return nil, nil
	}
	if runtime.server.eventJournal == nil || source.legacySessionID == "" {
		return nil, workspace.ErrExtractionSourceChanged
	}
	entries, err := runtime.server.eventJournal.ReadAllStrict(source.legacySessionID)
	if err != nil {
		return nil, fmt.Errorf("read memory extraction coverage journal: %w", err)
	}
	if _, err := legacyMemorySourceEntries(entries, source); err != nil {
		return nil, workspace.ErrExtractionSourceChanged
	}
	return entries, nil
}

func (runtime *memoryExtractionRuntime) readMemoryCoverageSnapshot(ctx context.Context, sessionID string, current memoryExtractionSource, candidate workspace.CompletedExtractionSource, legacy []eventjournal.Entry) (memoryExtractionJournalSnapshot, bool, error) {
	if proof := candidate.Transcript; proof != nil {
		actual, err := runtime.server.transcriptStore.ReadCompletedMemorySource(ctx, proof.StreamUID, proof.OwnerID, proof.EventID)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, transcriptstore.ErrEventConflict) {
			return memoryExtractionJournalSnapshot{}, false, nil
		}
		if err != nil {
			return memoryExtractionJournalSnapshot{}, false, err
		}
		expected := *proof
		expected.BranchID, expected.BranchGeneration = actual.BranchID, actual.BranchGeneration
		if actual != expected || current.proof == nil {
			return memoryExtractionJournalSnapshot{}, false, workspace.ErrExtractionPlanConflict
		}
		if actual.BranchID != current.proof.BranchID || actual.BranchGeneration != current.proof.BranchGeneration {
			return memoryExtractionJournalSnapshot{}, false, workspace.ErrExtractionSourceChanged
		}
		snapshot, err := runtime.readMemoryExtractionSnapshot(ctx, actual.OwnerID, sessionID,
			memoryExtractionSource{proof: &actual, finishEventID: actual.EventID, captured: true})
		return snapshot, err == nil, err
	}
	if candidate.Legacy == nil || current.proof != nil || candidate.Legacy.SessionID != current.legacySessionID {
		return memoryExtractionJournalSnapshot{}, false, workspace.ErrExtractionPlanConflict
	}
	digest, err := hex.DecodeString(candidate.Legacy.PrefixSHA256)
	if err != nil || len(digest) != len(current.legacyPrefixSHA256) {
		return memoryExtractionJournalSnapshot{}, false, workspace.ErrExtractionPlanConflict
	}
	source := memoryExtractionSource{finishEventID: candidate.Legacy.FinishEventID,
		legacySessionID: candidate.Legacy.SessionID, legacyIncarnation: candidate.Legacy.FrameIncarnationID}
	copy(source.legacyPrefixSHA256[:], digest)
	prefix, err := legacyMemorySourceEntries(legacy, source)
	if err != nil {
		// A valid old immutable prefix may no longer belong to this journal.
		return memoryExtractionJournalSnapshot{}, false, nil
	}
	return projectMemoryExtractionSnapshot(prefix), true, nil
}
