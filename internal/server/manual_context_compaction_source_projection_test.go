package server

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestManualContextCompactionSourcePreservesPayloadAndArtifactProjection(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	appendArchiveThroughputArtifactsForTest(t, f, 2)
	finishSkillControlInput(t, f)
	request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
	input := transcriptstore.ListProjectedEventsInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		BranchID: request.Expected.BranchID, BranchGeneration: request.Expected.BranchGeneration,
		ThroughPublicationSequence: request.Expected.ThroughPublicationSeq, Limit: 1000}
	full, err := f.repo.ListProjectedEvents(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	coordinates, err := f.repo.ListProjectedCoordinateEvents(ctx, input)
	if err != nil || len(coordinates) != len(full) {
		t.Fatalf("canonical source size changed: full=%d coordinates=%d err=%v", len(full), len(coordinates), err)
	}
	refs := 0
	for index, projected := range full {
		refs += len(projected.ArtifactReferences)
		if !reflect.DeepEqual(projected.Event, coordinates[index].Event) || !bytes.Equal(projected.ResolvedPayloadJSON, coordinates[index].ResolvedPayloadJSON) {
			t.Fatal("lightweight projection changed the canonical event or resolved payload")
		}
	}
	if refs != 4 {
		t.Fatalf("normal projection lost original source and terminal artifact references: %d", refs)
	}
	entries, err := f.server.contextCompactionSource(ctx, request, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.SourceEventType == "assistant_message" {
			found = entry.Message["text"] == "Synthetic evidence retained." && entry.Message["role"] == "assistant"
		}
	}
	if !found {
		t.Fatal("compaction source lost the original assistant content")
	}
	again, err := f.repo.ListProjectedEvents(ctx, input)
	if err != nil || !reflect.DeepEqual(full, again) {
		t.Fatal("reading compaction source changed the normal artifact projection")
	}
}
