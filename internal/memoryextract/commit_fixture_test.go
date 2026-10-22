package memoryextract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"

	eventjournal "x-science/internal/persistence/journal"
	workspace "x-science/internal/persistence/workspace"
)

// bindExtractionSource records an actual completed legacy journal prefix. The
// returned input retains that journal and source across retries; another call
// creates a separate logical completion rather than reusing a saved receipt.
func bindExtractionSource(t *testing.T, store *workspace.Store, input RunInput) RunInput {
	t.Helper()
	frame, found, err := store.GetFrame(input.Scope.SourceFrameID)
	if err != nil || !found || frame.IncarnationID == "" || frame.ProjectID != input.Scope.ProjectID {
		t.Fatalf("extraction source frame = %#v, found=%v, err=%v", frame, found, err)
	}
	journal := eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "journal"))
	for _, message := range input.Messages {
		if _, err := journal.Append(frame.ID, eventjournal.Message{
			"type": "message", "role": message.Role, "content": message.Content,
		}, eventjournal.Metadata{}); err != nil {
			t.Fatal(err)
		}
	}
	finish, err := journal.Append(frame.ID, eventjournal.Message{
		"type": "runner_finished", "status": "completed", "runnerAttempt": 1,
		"finalResponseText": input.FinalResponseText,
	}, eventjournal.Metadata{})
	if err != nil || finish == nil {
		t.Fatalf("record completed extraction source = %#v, %v", finish, err)
	}
	entries, err := journal.ReadAllStrict(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	prefixDigest, err := extractionFixturePrefixDigest(entries, finish.EventID)
	if err != nil {
		t.Fatal(err)
	}
	input.Source = &workspace.CompletedExtractionSource{
		Schema: 1, OwnerUserID: input.Scope.UserID, ProjectID: input.Scope.ProjectID, FrameID: frame.ID,
		Legacy: &workspace.LegacyExtractionSource{
			SessionID: frame.ID, FrameIncarnationID: frame.IncarnationID,
			FinishEventID: finish.EventID, PrefixSHA256: prefixDigest,
		},
	}
	input.CommitGuard = func(ctx context.Context, commit func() error) error {
		return journal.WithStrictSnapshot(ctx, frame.ID, func(current []eventjournal.Entry) error {
			digest, err := extractionFixturePrefixDigest(current, finish.EventID)
			if err != nil || digest != prefixDigest {
				return workspace.ErrExtractionSourceChanged
			}
			return commit()
		})
	}
	return input
}

func extractionFixturePrefixDigest(entries []eventjournal.Entry, finishEventID int64) (string, error) {
	prefix := make([]eventjournal.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.EventID > finishEventID {
			break
		}
		prefix = append(prefix, entry)
		if entry.EventID == finishEventID {
			if entry.Message["type"] != "runner_finished" || entry.Message["status"] != "completed" {
				return "", workspace.ErrExtractionSourceChanged
			}
			encoded, err := json.Marshal(prefix)
			if err != nil {
				return "", err
			}
			digest := sha256.Sum256(encoded)
			return hex.EncodeToString(digest[:]), nil
		}
	}
	return "", workspace.ErrExtractionSourceChanged
}

// applyPreparedExtraction keeps old mutation-effect assertions on the real
// atomic commit path without retaining a production incremental Apply API.
func applyPreparedExtraction(t *testing.T, ctx context.Context, service *Service, scope ApplyScope, operations Operations) (ApplyResult, error) {
	t.Helper()
	plan, err := service.Prepare(ctx, scope, operations)
	if err != nil || plan.Prepared.PIUnavailable {
		return plan.Prepared, err
	}
	encoded, err := json.Marshal(operations)
	if err != nil {
		return ApplyResult{}, err
	}
	input := bindExtractionSource(t, service.store, RunInput{
		Scope: scope,
		Messages: []Message{{Role: "user", Content: []Block{{
			Type: BlockText, Text: string(encoded),
		}}}},
	})
	cursor, err := service.store.LastExtractMessageIndex(ctx, scope.SourceFrameID)
	if err != nil {
		return ApplyResult{}, err
	}
	digest := sha256.Sum256(encoded)
	var committed workspace.ExtractionCommitResult
	err = input.CommitGuard(ctx, func() error {
		var commitErr error
		committed, commitErr = service.store.CommitMemoryExtraction(ctx, workspace.ExtractionCommitInput{
			Source: *input.Source, InputSHA256: hex.EncodeToString(digest[:]),
			CursorBefore: cursor, CursorNext: cursor, DeltaCount: 1, Gate: "done", Plan: plan,
		})
		return commitErr
	})
	return committed.Applied, err
}
