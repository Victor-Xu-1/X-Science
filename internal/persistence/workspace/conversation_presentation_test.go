package workspace

import (
	"context"
	"errors"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestConversationPresentationNarrowCASPreservesRuntimeAndActivity(t *testing.T) {
	store, _, stream := newTranscriptCancellationFixtureWithoutRunnerClaim(t)
	frame, _, _ := store.GetFrame(stream.FrameID)
	metadata, _, err := store.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ContextData == nil {
		metadata.ContextData = map[string]any{}
	}
	metadata.ContextData["runtime_owned"] = "before"
	if _, err := store.SetFrameRuntimeMetadata(frame.ID, metadata); err != nil {
		t.Fatal(err)
	}
	frame, _, _ = store.GetFrame(frame.ID)
	revision := int64(0)
	input := ConversationPresentationUpdate{OwnerID: stream.OwnerID, FrameID: frame.ID, IncarnationID: frame.IncarnationID, ExpectedRevision: &revision, Extra: map[string]any{"pinned": true}}
	if _, err := store.UpdateConversationPresentation(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	after, _, _ := store.GetFrame(frame.ID)
	if !after.UpdatedAt.Equal(frame.UpdatedAt) {
		t.Fatal("pin changed recent activity")
	}
	stored, _, _ := store.GetFrameRuntimeMetadata(frame.ID)
	view, err := ConversationPresentation(stored.ContextData)
	if err != nil || view["pinned"] != true || view["presentation_revision"] != int64(1) || stored.ContextData["runtime_owned"] != "before" {
		t.Fatalf("presentation=%#v runtime=%#v err=%v", view, stored, err)
	}
	// A runtime writer holding a pre-pin snapshot must not erase presentation.
	metadata.ContextData["runtime_owned"] = "after"
	if _, err := store.SetFrameRuntimeMetadata(frame.ID, metadata); err != nil {
		t.Fatal(err)
	}
	stored, _, _ = store.GetFrameRuntimeMetadata(frame.ID)
	view, err = ConversationPresentation(stored.ContextData)
	if err != nil || view["pinned"] != true || stored.ContextData["runtime_owned"] != "after" {
		t.Fatalf("stale runtime overwrite: %#v %v", stored, err)
	}
	if changed, err := store.UpdateConversationPresentation(context.Background(), input); err != nil || changed {
		t.Fatalf("same desired retry changed=%t err=%v", changed, err)
	}
	input.Extra = map[string]any{"pinned": false}
	if _, err := store.UpdateConversationPresentation(context.Background(), input); !errors.Is(err, ErrConversationPresentationStale) {
		t.Fatalf("stale overwrite: %v", err)
	}
	input.IncarnationID = "replacement"
	input.Extra = map[string]any{"pinned": true}
	if _, err := store.UpdateConversationPresentation(context.Background(), input); !errors.Is(err, ErrConversationPresentationStale) {
		t.Fatalf("ABA no-op: %v", err)
	}
	input.IncarnationID = frame.IncarnationID
	input.OwnerID = "other-owner"
	if _, err := store.UpdateConversationPresentation(context.Background(), input); !errors.Is(err, transcriptstore.ErrOwnerMismatch) {
		t.Fatalf("owner: %v", err)
	}
}

func TestConversationPresentationBatchReorderIsAtomic(t *testing.T) {
	store, _, stream := newTranscriptCancellationFixtureWithoutRunnerClaim(t)
	first, _, _ := store.GetFrame(stream.FrameID)
	second, err := store.CreateFrame(CreateFrameInput{ID: "pin-second", ProjectID: first.ProjectID, AgentName: "agent", Status: "completed", ConversationType: "agent", Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	for _, frame := range []Frame{first, second} {
		if _, err := store.UpdateConversationPresentation(context.Background(), ConversationPresentationUpdate{OwnerID: stream.OwnerID, FrameID: frame.ID, IncarnationID: frame.IncarnationID, ExpectedRevision: &zero, Extra: map[string]any{"pinned": true}}); err != nil {
			t.Fatal(err)
		}
	}
	items := []ConversationPinnedOrderItem{{ID: first.ID, IncarnationID: first.IncarnationID, ExpectedRevision: 1, SortOrder: 2000}, {ID: second.ID, IncarnationID: second.IncarnationID, ExpectedRevision: 0, SortOrder: 1000}}
	if _, err := store.ReorderPinnedConversations(context.Background(), stream.OwnerID, first.ID, items); !errors.Is(err, ErrConversationPresentationStale) {
		t.Fatalf("stale batch: %v", err)
	}
	metadata, _, _ := store.GetFrameRuntimeMetadata(first.ID)
	view, _ := ConversationPresentation(metadata.ContextData)
	if _, found := view["sortOrder"]; found {
		t.Fatal("batch partially committed")
	}
	items[1].ExpectedRevision = 1
	if _, err := store.ReorderPinnedConversations(context.Background(), stream.OwnerID, first.ID, items); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReorderPinnedConversations(context.Background(), stream.OwnerID, first.ID, items); err != nil {
		t.Fatalf("lost receipt retry: %v", err)
	}
}

func TestConversationPresentationRejectsOpaqueLegacyExtraWithoutReplacingIt(t *testing.T) {
	store, _, stream := newTranscriptCancellationFixtureWithoutRunnerClaim(t)
	frame, _, _ := store.GetFrame(stream.FrameID)
	metadata, _, err := store.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ContextData == nil {
		metadata.ContextData = map[string]any{}
	}
	metadata.ContextData["web_extra"] = "opaque-legacy-presentation"
	metadata.ContextData["runtime_owned"] = "keep"
	if _, err := store.SetFrameRuntimeMetadata(frame.ID, metadata); err != nil {
		t.Fatal(err)
	}
	before, _, _ := store.GetFrame(frame.ID)
	zero := int64(0)
	if _, err := store.UpdateConversationPresentation(context.Background(), ConversationPresentationUpdate{OwnerID: stream.OwnerID, FrameID: frame.ID, IncarnationID: frame.IncarnationID, ExpectedRevision: &zero, Extra: map[string]any{"pinned": true}}); !errors.Is(err, ErrConversationPresentationInvalid) {
		t.Fatalf("opaque legacy accepted: %v", err)
	}
	stored, _, _ := store.GetFrameRuntimeMetadata(frame.ID)
	after, _, _ := store.GetFrame(frame.ID)
	if stored.ContextData["web_extra"] != "opaque-legacy-presentation" || stored.ContextData["runtime_owned"] != "keep" || !before.UpdatedAt.Equal(after.UpdatedAt) {
		t.Fatalf("rejected write changed metadata: %#v", stored)
	}
}
