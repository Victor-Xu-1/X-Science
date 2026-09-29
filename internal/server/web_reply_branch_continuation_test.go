package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestWebReplyBranchContinuationCitesInheritedVersionWithoutClaimingProduction(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	first := saveReplyBranchArtifact(t, f, "continuation-first", "continued.txt")
	finishRoundPresentation(t, f, "completed")
	child := cloneReplyForTest(t, f, f.stream.FrameID, f.claim.Attempt)
	startNextPresentationRound(t, f)
	later := saveReplyBranchArtifact(t, f, "continuation-later", "continued.txt")
	finishRoundPresentation(t, f, "completed")
	stream, found, err := f.repo.GetFrameStreamBySession(context.Background(), f.stream.OwnerID, child)
	if err != nil || !found {
		t.Fatal(err)
	}
	f.stream = stream
	startNextPresentationRound(t, f)
	for _, tc := range []struct {
		name     string
		artifact map[string]any
		relation transcriptstore.ArtifactRelation
	}{
		{"future-citation", later, transcriptstore.ArtifactRelationCited},
		{"false-production", first, transcriptstore.ArtifactRelationProduced},
		{"false-attachment", first, transcriptstore.ArtifactRelationAttached},
	} {
		_, _, _, err := f.repo.AppendAssistantEventWithArtifacts(context.Background(), transcriptstore.AppendAssistantEventWithArtifactsInput{
			Claim: f.claim, ClientMessageID: tc.name, Source: transcriptstore.EventSourcePayload,
			PayloadJSON: []byte(`{"text":"invalid reference"}`),
			References:  []transcriptstore.ArtifactReferenceInput{{ArtifactID: webString(tc.artifact["artifact_id"]), VersionID: webString(tc.artifact["version_id"]), Relation: tc.relation}},
		})
		if !errors.Is(err, transcriptstore.ErrArtifactMismatch) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"text": "Use the inherited result {{artifact:" + webString(first["version_id"]) + "}}"})
	if err := f.server.appendTranscriptAssistantEventWithTurnArtifacts(context.Background(), &transcriptRunnerAuthority{Stream: stream, Claim: f.claim}, transcriptstore.AppendEventInput{
		Claim: f.claim, Type: "assistant_message", ClientMessageID: "valid-inherited-citation", Source: transcriptstore.EventSourcePayload, PayloadJSON: raw,
	}); err != nil {
		t.Fatalf("independent continuation cannot cite inherited file: %v", err)
	}
	if _, _, _, err := f.repo.AppendAssistantEventWithArtifacts(context.Background(), transcriptstore.AppendAssistantEventWithArtifactsInput{
		Claim: f.claim, ClientMessageID: "inherited-input-used", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: []byte(`{"text":"Used inherited input"}`),
		References:  []transcriptstore.ArtifactReferenceInput{{ArtifactID: webString(first["artifact_id"]), VersionID: webString(first["version_id"]), Relation: transcriptstore.ArtifactRelationConsumed}},
	}); err != nil {
		t.Fatalf("inherited input consumption: %v", err)
	}
	finishRoundPresentation(t, f, "completed")
}
