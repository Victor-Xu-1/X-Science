package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func appendArchiveThroughputArtifactsForTest(t *testing.T, f *agentSaveArtifactsFixture, count int) {
	t.Helper()
	ctx := context.Background()
	refs := make([]transcriptstore.ArtifactReferenceInput, 0, count)
	for index := 0; index < count; index++ {
		if index%100 == 0 {
			if _, err := f.repo.HeartbeatRunner(ctx, transcriptstore.HeartbeatRunnerInput{Claim: f.claim, TTL: time.Minute}); err != nil {
				t.Fatal(err)
			}
		}
		artifact, version, err := f.store.WriteArtifactVersion(ctx, workspace.WriteArtifactVersionInput{
			ArtifactID: fmt.Sprintf("archive-evidence-%d", index), ProjectID: f.stream.ProjectID,
			Name: "Synthetic evidence.txt", ContentType: "text/plain", Content: strings.NewReader("Synthetic evidence."),
			CreatedBy: f.stream.OwnerID, RootFrameID: f.stream.RootFrameID, FrameID: f.stream.FrameID})
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, transcriptstore.ArtifactReferenceInput{ArtifactID: artifact.ID,
			VersionID: version.ID, Relation: transcriptstore.ArtifactRelationCited})
	}
	for batch, after := 0, 0; after < len(refs); batch++ {
		end := min(after+256, len(refs))
		_, associated, created, err := f.repo.AppendAssistantEventWithArtifacts(ctx, transcriptstore.AppendAssistantEventWithArtifactsInput{
			Claim: f.claim, ClientMessageID: fmt.Sprintf("archive-source-artifacts-%d", batch), Source: transcriptstore.EventSourcePayload,
			PayloadJSON: []byte(`{"text":"Synthetic evidence retained."}`), References: refs[after:end]})
		if err != nil || !created || len(associated) != end-after {
			t.Fatalf("canonical association created=%t refs=%d err=%v", created, len(associated), err)
		}
		after = end
	}
}
