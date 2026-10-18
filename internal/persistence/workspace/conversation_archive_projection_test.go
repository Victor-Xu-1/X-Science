package workspace

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestConversationArchiveReadOnlyFlagIsNotInheritedByMergeOrClone(t *testing.T) {
	store, _ := newConversationArchiveFixture(t)
	frame := conversationArchiveFixtureFrame(t, store, "archive-read-only-flag", 1, false, false)
	metadata, _, err := store.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata.ContextData["web_extra"].(map[string]any)["presentation_unavailable"] = true
	if _, err := store.SetFrameRuntimeMetadata(frame.ID, metadata); err != nil {
		t.Fatal(err)
	}
	revision := int64(7)
	if changed, err := store.UpdateConversationPresentation(context.Background(), ConversationPresentationUpdate{
		OwnerID: "archive-owner", FrameID: frame.ID, IncarnationID: frame.IncarnationID,
		ExpectedRevision: &revision, Extra: map[string]any{"pinned": true},
	}); err != nil || !changed {
		t.Fatalf("real Pin merge changed=%t err=%v", changed, err)
	}
	stored, _, err := store.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	extra := stored.ContextData["web_extra"].(map[string]any)
	if _, present := extra["presentation_unavailable"]; present || extra["pinned"] != true || extra[presentationRevisionKey] != float64(8) || stored.ContextData["runtime_owned"] != "retain" {
		t.Fatalf("merge retained projection or lost real metadata: %#v", stored)
	}
	revision = 8
	if _, err := store.UpdateConversationPresentation(context.Background(), ConversationPresentationUpdate{
		OwnerID: "archive-owner", FrameID: frame.ID, IncarnationID: frame.IncarnationID,
		ExpectedRevision: &revision, Extra: map[string]any{"presentation_unavailable": false},
	}); !errors.Is(err, ErrConversationPresentationInvalid) {
		t.Fatalf("ordinary patch may write a read-only projection: %v", err)
	}
	// A legacy stored projection must not be copied to a new identity. The
	// clone scrub remains read-only and preserves unrelated source metadata.
	extra["presentation_unavailable"] = true
	source := map[string]any{"web_extra": extra, "runtime_owned": "retain"}
	before := map[string]any{"web_extra": map[string]any{}, "runtime_owned": "retain"}
	for key, value := range extra {
		before["web_extra"].(map[string]any)[key] = value
	}
	cloned := cloneConversationPresentationContext(source)
	if _, present := cloned["web_extra"].(map[string]any)["presentation_unavailable"]; present || cloned["runtime_owned"] != "retain" || !reflect.DeepEqual(source, before) {
		t.Fatalf("clone retained computed projection or changed source: source=%#v cloned=%#v", source, cloned)
	}
}
