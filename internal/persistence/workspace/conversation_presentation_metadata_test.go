package workspace

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestConversationPresentationNameOnlyChecksIdentityWithoutTouchingExtra(t *testing.T) {
	store, _, stream := newTranscriptCancellationFixtureWithoutRunnerClaim(t)
	frame, _, err := store.GetFrame(stream.FrameID)
	if err != nil {
		t.Fatal(err)
	}
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
	name, description := "Renamed", "Updated description"
	input := ConversationPresentationUpdate{
		OwnerID: stream.OwnerID, FrameID: frame.ID, IncarnationID: frame.IncarnationID,
		Name: &name, Description: &description,
	}
	for _, test := range []struct {
		owner, incarnation string
		want               error
	}{
		{stream.OwnerID, "previous-incarnation", ErrConversationPresentationStale},
		{"other-owner", frame.IncarnationID, transcriptstore.ErrOwnerMismatch},
	} {
		invalid := input
		invalid.OwnerID, invalid.IncarnationID = test.owner, test.incarnation
		if _, err := store.UpdateConversationPresentation(context.Background(), invalid); !errors.Is(err, test.want) {
			t.Fatalf("identity mismatch returned %v, want %v", err, test.want)
		}
	}
	unchanged, _, _ := store.GetFrame(frame.ID)
	if unchanged.Name != before.Name || !unchanged.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("rejected identity changed frame: %#v", unchanged)
	}
	empty := input
	empty.Name, empty.Description = nil, nil
	if _, err := store.UpdateConversationPresentation(context.Background(), empty); !errors.Is(err, ErrConversationPresentationInvalid) {
		t.Fatalf("empty update accepted: %v", err)
	}
	now := before.UpdatedAt.Add(time.Minute)
	store.now = func() time.Time { return now }
	if changed, err := store.UpdateConversationPresentation(context.Background(), input); err != nil || changed {
		t.Fatalf("metadata-only update: presentationChanged=%t err=%v", changed, err)
	}
	after, _, err := store.GetCompatibilityFrame(frame.ID)
	if err != nil || after.Name != name || after.TaskSummary != description || !after.UpdatedAt.Equal(now) {
		t.Fatalf("metadata update lost original activity semantics: %#v %v", after, err)
	}
	stored, _, _ := store.GetFrameRuntimeMetadata(frame.ID)
	if !reflect.DeepEqual(stored.ContextData, metadata.ContextData) {
		t.Fatalf("metadata-only update changed context: %#v", stored.ContextData)
	}
}
