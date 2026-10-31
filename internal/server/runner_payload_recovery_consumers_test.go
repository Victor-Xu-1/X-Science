package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestPayloadOnlyRecoveryConsumersRestorePagedStateWithoutArtifactJoins(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	artifact := seedTranscriptWebReadModelArtifact(t, f.store, f.db, f.stream.ProjectID, f.stream.FrameID,
		"recovery-reference", "reference.txt", []byte("an unrelated saved artifact"), f.stream.OwnerID)
	if _, refs, created, err := f.repo.AppendAssistantEventWithArtifacts(ctx, transcriptstore.AppendAssistantEventWithArtifactsInput{
		Claim: f.claim, ClientMessageID: "earlier-artifact", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: []byte(`{"text":"earlier result"}`),
		References:  []transcriptstore.ArtifactReferenceInput{{ArtifactID: artifact.artifactID, VersionID: artifact.versionID, Relation: transcriptstore.ArtifactRelationProduced}},
	}); err != nil || !created || len(refs) != 1 {
		t.Fatalf("real artifact receipt created=%t refs=%v err=%v", created, refs, err)
	}
	for index := 0; index < 1001; index++ {
		appendPayloadRecoveryCheckpoint(t, f, fmt.Sprintf("filler-%04d", index), map[string]any{"status": "running", "index": index})
	}
	appendPayloadRecoveryCheckpoint(t, f, "selection-after-first-page", map[string]any{
		"status": "completed", "toolPhase": "completed", "toolName": "manage_environments",
		"toolResult": map[string]any{"ok": true, "implementation_selection": map[string]any{
			"provenance": "registry-unique-local-pack", "implementations": []string{"Synthetic Engine"},
		}},
	})
	run := payloadRecoveryTestRun(f)
	const retained = "accepted content beyond the first projection page"
	if err := f.server.checkpointChatContentDelta(ctx, SessionRunnerChatOptions{}, run, retained, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.persistProviderContinuationBoundary(ctx, run); err != nil {
		t.Fatal(err)
	}
	appendPayloadRecoveryCheckpoint(t, f, "typed-interruption", map[string]any{
		"status": "interrupted", "reason_code": "provider_stream_interrupted",
	})
	baselineRun := payloadRecoveryTestRun(f)
	if err := f.server.loadProviderContinuation(ctx, baselineRun); err != nil {
		t.Fatalf("baseline continuation: %v", err)
	}
	baselineChoices, err := f.server.runnerAskUserSelectionEntries(ctx, baselineRun)
	if err != nil || !reflect.DeepEqual(selectedAskUserImplementationsFromRunnerEntries(baselineChoices), []string{"Synthetic Engine"}) {
		t.Fatalf("baseline authority=%v err=%v", baselineChoices, err)
	}
	snapshot, err := f.repo.GetProjectionSnapshot(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	count, refs := 0, 0
	if err := f.server.scanTranscriptProjection(ctx, snapshot, f.stream.OwnerID, snapshot.ThroughPublicationSequence, func(event transcriptstore.ProjectedEvent) error {
		count++
		refs += len(event.ArtifactReferences)
		return nil
	}); err != nil || count <= 1000 || refs == 0 {
		t.Fatalf("fixture must exercise real multi-page full projection: events=%d refs=%d err=%v", count, refs, err)
	}
	// Only this disposable fixture loses the optional association table. A full
	// projection must fail, proving that successful recovery did not merely
	// fetch artifact metadata and discard it afterwards.
	if _, err := f.db.Exec(`ALTER TABLE artifact_versions RENAME TO unavailable_recovery_artifact_versions`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.Exec(`ALTER TABLE unavailable_recovery_artifact_versions RENAME TO artifact_versions`); err != nil {
			t.Errorf("restore disposable fixture: %v", err)
		}
	})
	err = f.server.scanTranscriptProjection(ctx, snapshot, f.stream.OwnerID, snapshot.ThroughPublicationSequence, func(transcriptstore.ProjectedEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "artifact_versions") {
		t.Fatalf("optional association dependency was not disabled: %v", err)
	}
	t.Run("continuation", func(t *testing.T) {
		restored := payloadRecoveryTestRun(f)
		if err := f.server.loadProviderContinuation(ctx, restored); err != nil {
			t.Fatalf("payload-only continuation attempted artifact association: %v", err)
		}
		if restored.ProviderContinuation == nil || restored.ProviderContinuation.Content.String() != retained ||
			!reflect.DeepEqual(restored.ProviderContinuation.Contract, baselineRun.ProviderContinuation.Contract) {
			t.Fatalf("accepted content/fence changed: %#v", restored.ProviderContinuation)
		}
	})
	t.Run("ask_user", func(t *testing.T) {
		choices, err := f.server.runnerAskUserSelectionEntries(ctx, payloadRecoveryTestRun(f))
		if err != nil || !reflect.DeepEqual(choices, baselineChoices) {
			t.Fatalf("payload-only choice replay changed or attempted artifact association: choices=%v err=%v", choices, err)
		}
	})
}

func payloadRecoveryTestRun(f *agentSaveArtifactsFixture) *sessionRunnerChatRun {
	return &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt),
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
}

func appendPayloadRecoveryCheckpoint(t *testing.T, f *agentSaveArtifactsFixture, id string, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := f.repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: id, Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: raw,
	}); err != nil || !created {
		t.Fatalf("append checkpoint %s created=%t err=%v", id, created, err)
	}
}
