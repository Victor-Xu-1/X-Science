package server

import (
	"context"
	"encoding/hex"

	"x-science/internal/memoryextract"
	eventjournal "x-science/internal/persistence/journal"
	workspace "x-science/internal/persistence/workspace"
)

// Called only under WithStrictSnapshot after verifying the current prefix.
// The checker passed into SQLite closes over that held snapshot; it performs
// no DB reads, lock acquisition or journal I/O. Event numbers alone are not
// lineage: replaced/renumbered journals must match their original prefix SHA.
func legacyMemoryExtractionOrderCheck(ctx context.Context, entries []eventjournal.Entry, scope memoryextract.ApplyScope, source memoryExtractionSource) workspace.LegacyExtractionOrderCheck {
	ends := make(map[int64]int)
	for index, entry := range entries {
		if entry.EventID > source.finishEventID && stringValue(entry.Message["type"]) == "runner_finished" &&
			stringValue(entry.Message["status"]) == "completed" {
			ends[entry.EventID] = index + 1
		}
	}
	digests := make(map[int64]string)
	return func(candidate workspace.CompletedExtractionSource) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		prior := candidate.Legacy
		if candidate.Schema != 1 || candidate.Transcript != nil || prior == nil ||
			candidate.OwnerUserID != scope.UserID || candidate.ProjectID != scope.ProjectID || candidate.FrameID != scope.SourceFrameID ||
			prior.SessionID != source.legacySessionID || prior.FrameIncarnationID != source.legacyIncarnation ||
			prior.FinishEventID <= source.finishEventID {
			return false, workspace.ErrExtractionPlanConflict
		}
		if raw, err := hex.DecodeString(prior.PrefixSHA256); err != nil || len(raw) != len(source.legacyPrefixSHA256) {
			return false, workspace.ErrExtractionPlanConflict
		}
		end, exists := ends[prior.FinishEventID]
		if !exists {
			return false, nil
		}
		digest, cached := digests[prior.FinishEventID]
		if !cached {
			actual, err := legacyMemoryPrefixDigest(entries[:end])
			if err != nil {
				return false, err
			}
			digest = hex.EncodeToString(actual[:])
			digests[prior.FinishEventID] = digest
		}
		return digest == prior.PrefixSHA256, nil
	}
}
