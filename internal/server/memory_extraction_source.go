package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

type memoryExtractionSource struct {
	finishEventID      int64
	attempt            int64
	proof              *transcriptstore.CompletedMemorySource
	legacySessionID    string
	legacyPrefixSHA256 [sha256.Size]byte
	request            *agentruntime.ModelRequest
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
		} else if err := runtime.captureLegacyMemorySource(sessionID, &source); err != nil {
			return source, err
		}
	} else if err := runtime.captureLegacyMemorySource(sessionID, &source); err != nil {
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

func (runtime *memoryExtractionRuntime) captureLegacyMemorySource(sessionID string, source *memoryExtractionSource) error {
	if runtime.server.eventJournal == nil {
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

// Revalidate after model completion and before the existing parser/apply/cursor
// path. A stale optional extraction never fails the already completed task.
func (runtime *memoryExtractionRuntime) sourceBoundMemoryExtractor(source memoryExtractionSource, delegate memoryextract.Extractor) memoryextract.Extractor {
	return memoryExtractorFunc(func(ctx context.Context, request memoryextract.ExtractRequest) (map[string]any, error) {
		result, err := delegate.ExtractMemories(ctx, request)
		if err != nil {
			return nil, err
		}
		if err := runtime.validateCompletedMemorySource(ctx, source); err != nil {
			return nil, err
		}
		return result, nil
	})
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
