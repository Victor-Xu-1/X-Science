package workspace

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestConversationPresentationCloneDoesNotInheritOrMutateSource(t *testing.T) {
	store, source := newCanonicalConversationCloneFixture(t)
	ctx := context.Background()
	zero := int64(0)
	if _, err := store.UpdateConversationPresentation(ctx, ConversationPresentationUpdate{OwnerID: "owner-a", FrameID: source.ID, IncarnationID: source.IncarnationID, ExpectedRevision: &zero, Extra: map[string]any{"pinned": true, "selected_view": "timeline"}}); err != nil {
		t.Fatal(err)
	}
	one := int64(1)
	if _, err := store.UpdateConversationPresentation(ctx, ConversationPresentationUpdate{OwnerID: "owner-a", FrameID: source.ID, IncarnationID: source.IncarnationID, ExpectedRevision: &one, Extra: map[string]any{"sortOrder": 1000}}); err != nil {
		t.Fatal(err)
	}
	before, _, err := store.GetFrameRuntimeMetadata(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before.ContextData)
	input := CloneConversationInput{OwnerUserID: "owner-a", SourceFrameID: source.ID, ExpectedSourceIncarnationID: source.IncarnationID, TargetFrameID: "presentation-clone", TargetName: "Presentation copy"}
	if _, err := store.CloneConversationWithTranscript(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCompatibilityAsideWithTranscript(ctx, CompatibilityAsideInput{ID: "presentation-aside", ParentRootFrameID: source.ID, Request: "Followup", AsSession: true}, "presentation-aside-input", nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"presentation-clone", "presentation-aside"} {
		metadata, _, err := store.GetFrameRuntimeMetadata(id)
		if err != nil {
			t.Fatal(err)
		}
		extra, ok := metadata.ContextData["web_extra"].(map[string]any)
		if !ok || extra["selected_view"] != "timeline" {
			t.Fatalf("preserved metadata: %#v", metadata)
		}
		for _, key := range []string{"pinned", "pinned_at", "sortOrder", presentationRevisionKey} {
			if _, found := extra[key]; found {
				t.Fatalf("%s inherited %s", id, key)
			}
		}
	}
	after, _, _ := store.GetFrameRuntimeMetadata(source.ID)
	afterJSON, _ := json.Marshal(after.ContextData)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("clone mutated source metadata")
	}
	two := int64(2)
	if _, err := store.UpdateConversationPresentation(ctx, ConversationPresentationUpdate{OwnerID: "owner-a", FrameID: source.ID, IncarnationID: source.IncarnationID, ExpectedRevision: &two, Extra: map[string]any{"pinned": false}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CloneConversationWithTranscript(ctx, input); err != nil {
		t.Fatalf("parent pin changed clone retry: %v", err)
	}
	legacy := map[string]any{"web_extra": "opaque-legacy", "runtime": map[string]any{"value": "retained"}}
	if got := cloneConversationPresentationContext(legacy); !reflect.DeepEqual(got, legacy) {
		t.Fatalf("legacy changed: %#v", got)
	}
	owned := map[string]any{"web_extra": map[string]any{"pinned": true, "selected_view": "timeline"}}
	_ = cloneConversationPresentationContext(owned)
	if owned["web_extra"].(map[string]any)["pinned"] != true {
		t.Fatal("scrub mutated original map")
	}
}
