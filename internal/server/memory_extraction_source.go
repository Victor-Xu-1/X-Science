package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

type memoryExtractionSource struct {
	finishEventID      int64
	attempt            int64
	proof              *transcriptstore.CompletedMemorySource
	legacySessionID    string
	legacyIncarnation  string
	legacyPrefixSHA256 [sha256.Size]byte
	request            *agentruntime.ModelRequest
	extractMode        string
	model              workspace.MemoryExtractionModelIdentity
	modeDecision       *workspace.MemoryExtractionModeDecision
	deliveryClaim      *workspace.MemoryExtractionDeliveryClaim
	cadence            *workspace.MemoryExtractionCadence
	captured           bool
}

var errMemoryExtractionSource = errors.New("completed memory source is unavailable or changed")

func (runtime *memoryExtractionRuntime) captureCompletedMemorySource(ctx context.Context, sessionID string, eventID int64) (memoryExtractionSource, error) {
	source := memoryExtractionSource{finishEventID: eventID}
	if eventID <= 0 {
		return source, errMemoryExtractionSource
	}
	resolved, err := runtime.resolveCompletedRootMemoryScope(ctx, sessionID)
	if err != nil {
		return source, errMemoryExtractionSource
	}
	if runtime.server.transcriptStore != nil {
		stream, found, err := runtime.server.transcriptStore.GetFrameStreamBySession(ctx, resolved.Scope.UserID, sessionID)
		if err != nil {
			return source, errMemoryExtractionSource
		}
		if found {
			proof, err := runtime.server.transcriptStore.ReadCompletedMemorySource(ctx, stream.UID, stream.OwnerID, eventID)
			if err != nil {
				return source, errMemoryExtractionSource
			}
			source.proof, source.attempt = &proof, proof.Attempt
		} else if err := runtime.captureLegacyMemorySource(ctx, sessionID, resolved.Scope.SourceFrameID, &source); err != nil {
			return source, err
		}
	} else if err := runtime.captureLegacyMemorySource(ctx, sessionID, resolved.Scope.SourceFrameID, &source); err != nil {
		return source, err
	}
	runtime.mu.Lock()
	if state := runtime.sessions[sessionID]; state != nil && state.requestAttempt == source.attempt && state.request != nil {
		request := agentruntime.SnapshotModelRequest(*state.request)
		source.request = &request
	}
	runtime.mu.Unlock()
	source.captured = true
	return source, nil
}

func (runtime *memoryExtractionRuntime) captureLegacyMemorySource(ctx context.Context, sessionID, frameID string, source *memoryExtractionSource) error {
	if runtime.server.eventJournal == nil {
		return errMemoryExtractionSource
	}
	access, found, err := runtime.server.workspaceStore.GetKernelFrameAccessContext(ctx, frameID)
	if err != nil || !found || access.Frame.IncarnationID == "" {
		return errMemoryExtractionSource
	}
	entries, err := runtime.server.eventJournal.ReadAllStrict(sessionID)
	if err != nil {
		return errMemoryExtractionSource
	}
	prefix, err := legacyMemoryCompletionPrefix(entries, source.finishEventID)
	if err != nil {
		return err
	}
	digest, err := legacyMemoryPrefixDigest(prefix)
	if err != nil {
		return err
	}
	source.legacySessionID, source.legacyPrefixSHA256 = sessionID, digest
	source.legacyIncarnation = access.Frame.IncarnationID
	// RunID is an optional caller label, not the runner attempt authority.
	if attempt, ok := computeReceiptInteger(prefix[len(prefix)-1].Message["runnerAttempt"]); ok && attempt > 0 {
		source.attempt = attempt
	}
	return nil
}

func (runtime *memoryExtractionRuntime) validateCompletedMemorySource(ctx context.Context, source memoryExtractionSource) error {
	if source.proof == nil {
		_, err := runtime.readLegacyMemorySource(source.legacySessionID, source)
		return err
	}
	actual, err := runtime.server.transcriptStore.ReadCompletedMemorySource(ctx, source.proof.StreamUID, source.proof.OwnerID, source.finishEventID)
	if err != nil || actual != *source.proof {
		return errMemoryExtractionSource
	}
	return nil
}

// This early post-model check avoids unnecessary preparation. Commit also
// revalidates the source after classification/repair inside its write fence.
func (runtime *memoryExtractionRuntime) sourceBoundMemoryExtractor(source memoryExtractionSource, delegate memoryextract.Extractor) memoryextract.Extractor {
	userID := ""
	if source.proof != nil {
		userID = source.proof.OwnerID
	}
	return memoryExtractorFunc(func(ctx context.Context, request memoryextract.ExtractRequest) (map[string]any, error) {
		if err := runtime.server.checkMemoryRecoveryPolicy(ctx, userID, source.modeDecision); err != nil {
			return nil, err
		}
		result, err := delegate.ExtractMemories(ctx, request)
		if err != nil {
			return nil, err
		}
		if err := runtime.validateCompletedMemorySource(ctx, source); err != nil {
			return nil, err
		}
		if err := runtime.server.checkMemoryRecoveryPolicy(ctx, userID, source.modeDecision); err != nil {
			return nil, err
		}
		return result, nil
	})
}

func (runtime *memoryExtractionRuntime) completedExtractionCommitSource(scope memoryextract.ApplyScope, source memoryExtractionSource) (workspace.CompletedExtractionSource, error) {
	bound := workspace.CompletedExtractionSource{
		Schema: 1, OwnerUserID: scope.UserID, ProjectID: scope.ProjectID, FrameID: scope.SourceFrameID,
	}
	if !source.captured {
		return bound, errMemoryExtractionSource
	}
	if source.proof != nil {
		proof := *source.proof
		bound.Transcript = &proof
	} else if source.legacySessionID != "" && source.legacyIncarnation != "" {
		bound.Legacy = &workspace.LegacyExtractionSource{
			SessionID: source.legacySessionID, FrameIncarnationID: source.legacyIncarnation,
			FinishEventID: source.finishEventID, PrefixSHA256: hex.EncodeToString(source.legacyPrefixSHA256[:]),
		}
	} else {
		return bound, errMemoryExtractionSource
	}
	return bound, nil
}

func (runtime *memoryExtractionRuntime) legacyExtractionCommitGuard(source memoryExtractionSource, scope memoryextract.ApplyScope, snapshotMessageCount int) func(context.Context, func(workspace.LegacyExtractionOrderCheck) error) error {
	if source.proof != nil {
		return nil
	}
	return func(ctx context.Context, commit func(workspace.LegacyExtractionOrderCheck) error) error {
		if runtime.server.eventJournal == nil {
			return errMemoryExtractionSource
		}
		return runtime.server.eventJournal.WithStrictSnapshot(ctx, source.legacySessionID, func(entries []eventjournal.Entry) error {
			if _, err := legacyMemorySourceEntries(entries, source); err != nil {
				return workspace.ErrExtractionSourceChanged
			}
			// A real history truncation may reset an overshooting cursor. An
			// older prefix of this still-selected history must not reset work
			// already committed for a later completion. The journal guard keeps
			// another legacy completion from racing this read and short commit;
			// the database also fences Runner's expected cursor in that commit.
			cursor, err := runtime.server.workspaceStore.LastExtractMessageIndex(ctx, scope.SourceFrameID)
			if err != nil {
				return err
			}
			if cursor > snapshotMessageCount {
				for _, entry := range entries {
					if entry.EventID > source.finishEventID && stringValue(entry.Message["type"]) == "runner_finished" && stringValue(entry.Message["status"]) == "completed" {
						return workspace.ErrExtractionSourceChanged
					}
				}
			}
			return commit(legacyMemoryExtractionOrderCheck(ctx, entries, scope, source))
		})
	}
}

func (runtime *memoryExtractionRuntime) extractionCommitGuard(source memoryExtractionSource, scope memoryextract.ApplyScope, snapshotMessageCount int) func(context.Context, func(workspace.LegacyExtractionOrderCheck) error) error {
	legacyGuard := runtime.legacyExtractionCommitGuard(source, scope, snapshotMessageCount)
	return func(ctx context.Context, commit func(workspace.LegacyExtractionOrderCheck) error) error {
		return runtime.server.withAutomaticMemoryCommit(ctx, scope.UserID, func() error {
			if legacyGuard != nil {
				return legacyGuard(ctx, commit)
			}
			return commit(nil)
		}, source.modeDecision)
	}
}

func (runtime *memoryExtractionRuntime) readLegacyMemorySource(sessionID string, source memoryExtractionSource) ([]eventjournal.Entry, error) {
	if runtime == nil || runtime.server == nil || runtime.server.eventJournal == nil ||
		sessionID == "" || source.legacySessionID != sessionID {
		return nil, errMemoryExtractionSource
	}
	entries, err := runtime.server.eventJournal.ReadAllStrict(sessionID)
	if err != nil {
		return nil, errMemoryExtractionSource
	}
	return legacyMemorySourceEntries(entries, source)
}

func legacyMemorySourceEntries(entries []eventjournal.Entry, source memoryExtractionSource) ([]eventjournal.Entry, error) {
	if source.legacySessionID == "" {
		return nil, errMemoryExtractionSource
	}
	prefix, err := legacyMemoryCompletionPrefix(entries, source.finishEventID)
	if err != nil {
		return nil, err
	}
	digest, err := legacyMemoryPrefixDigest(prefix)
	if err != nil || digest != source.legacyPrefixSHA256 {
		return nil, errMemoryExtractionSource
	}
	return prefix, nil
}

// The existing strict snapshot is fingerprinted only through this completion.
// Appending later inputs must not invalidate the captured completed history.
func legacyMemoryPrefixDigest(entries []eventjournal.Entry) ([sha256.Size]byte, error) {
	raw, err := json.Marshal(entries)
	if err != nil {
		return [sha256.Size]byte{}, errMemoryExtractionSource
	}
	return sha256.Sum256(raw), nil
}

func legacyMemoryCompletionPrefix(entries []eventjournal.Entry, eventID int64) ([]eventjournal.Entry, error) {
	for index, entry := range entries {
		if entry.EventID == eventID {
			if stringValue(entry.Message["type"]) != "runner_finished" || stringValue(entry.Message["status"]) != "completed" {
				return nil, errMemoryExtractionSource
			}
			return entries[:index+1], nil
		}
	}
	return nil, errMemoryExtractionSource
}
