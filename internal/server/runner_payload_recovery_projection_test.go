package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestPayloadRecoveryProjectionKeepsPagedPayloadAndAuthorityFences(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	for index := 0; index < 1001; index++ {
		appendPayloadRecoveryCheckpoint(t, f, fmt.Sprintf("page-%04d", index), map[string]any{"status": "running", "index": index})
	}
	snapshot, err := f.repo.GetProjectionSnapshot(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	var full []transcriptstore.ProjectedEvent
	if err := f.server.scanTranscriptProjection(ctx, snapshot, f.stream.OwnerID, snapshot.ThroughPublicationSequence, func(event transcriptstore.ProjectedEvent) error {
		event.ArtifactReferences = nil
		full = append(full, event)
		return nil
	}); err != nil || len(full) <= 1000 {
		t.Fatalf("multi-page baseline events=%d err=%v", len(full), err)
	}
	appendPayloadRecoveryCheckpoint(t, f, "after-accepted-fence", map[string]any{"status": "running"})
	collect := func(ctx context.Context, selected transcriptstore.ProjectionSnapshot, owner string, visit func(int) error) ([]transcriptstore.ProjectedEvent, error) {
		var events []transcriptstore.ProjectedEvent
		err := f.server.scanTranscriptPayloadProjection(ctx, selected, owner, selected.ThroughPublicationSequence, func(event transcriptstore.ProjectedEvent) error {
			event.ArtifactReferences = nil
			events = append(events, event)
			if visit != nil {
				return visit(len(events))
			}
			return nil
		})
		return events, err
	}
	t.Run("payload_and_accepted_cursor", func(t *testing.T) {
		got, err := collect(ctx, snapshot, f.stream.OwnerID, nil)
		if err != nil || !reflect.DeepEqual(got, full) {
			t.Fatalf("event/payload changed or later append crossed accepted fence: events=%d err=%v", len(got), err)
		}
	})
	t.Run("foreign_owner", func(t *testing.T) {
		got, err := collect(ctx, snapshot, "another-owner", nil)
		if !errors.Is(err, transcriptstore.ErrOwnerMismatch) || len(got) != 0 {
			t.Fatalf("owner fence events=%d err=%v", len(got), err)
		}
	})
	t.Run("stale_generation", func(t *testing.T) {
		stale := snapshot
		stale.BranchGeneration++
		got, err := collect(ctx, stale, f.stream.OwnerID, nil)
		if !errors.Is(err, transcriptstore.ErrBranchStateStale) || len(got) != 0 {
			t.Fatalf("branch generation fence events=%d err=%v", len(got), err)
		}
	})
	t.Run("cancel_between_pages", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		defer cancel()
		got, err := collect(cancelled, snapshot, f.stream.OwnerID, func(count int) error {
			if count == 1000 {
				cancel()
			}
			return nil
		})
		if !errors.Is(err, context.Canceled) || len(got) != 1000 {
			t.Fatalf("cancellation did not stop the next page: events=%d err=%v", len(got), err)
		}
	})
	t.Run("visitor_error", func(t *testing.T) {
		sentinel := errors.New("consumer declined event")
		got, err := collect(ctx, snapshot, f.stream.OwnerID, func(int) error { return sentinel })
		if !errors.Is(err, sentinel) || len(got) != 1 {
			t.Fatalf("visitor error lost: events=%d err=%v", len(got), err)
		}
	})
	t.Run("fresh_snapshot_after_cancel_and_append", func(t *testing.T) {
		fresh, err := f.repo.GetProjectionSnapshot(ctx, f.stream.UID, f.stream.OwnerID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := collect(ctx, fresh, f.stream.OwnerID, nil)
		if err != nil || len(got) != len(full)+1 || got[len(got)-1].Event.ClientMessageID != "after-accepted-fence" {
			t.Fatalf("fresh read reused an old/cancelled projection: events=%d err=%v", len(got), err)
		}
	})
}
