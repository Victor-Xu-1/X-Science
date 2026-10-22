package server

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	settingsstore "x-science/internal/persistence/settings"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestMemoryRuntimeRecapturedCanonicalCompletionRestoresOriginalReceipt(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := t.Context()
	f.server.settingsStore = settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
	f.server.memoryConfig.Enabled, f.server.memoryConfig.ExtractEnabled = true, true
	f.server.memoryConfig.ExtractMode, f.server.memoryConfig.PIClassifierEnabled = "haiku", false
	if err := f.store.SetMemoryEnabled(ctx, f.stream.OwnerID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.repo.AppendRunnerEvent(ctx, transcriptstore.AppendEventInput{
		Claim: f.claim, ClientMessageID: "recaptured-answer", Type: "assistant_message", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: []byte(`{"role":"assistant","text":"Reports preserve explicit reproducibility settings."}`),
	}); err != nil {
		t.Fatal(err)
	}
	finish, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: f.claim, ClientMessageID: "recaptured-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMemoryExtractionRuntimeWithFactory(f.server, func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
			return map[string]any{"append": []any{map[string]any{"text": "Reports preserve explicit reproducibility settings.", "evidence": "stated"}}}, nil
		})
	})
	original, err := runtime.RunCompletedRoot(ctx, f.stream.SessionID, finish.EventID)
	if err != nil || original.Gate != "done" || len(original.Applied.Appended) != 1 {
		t.Fatalf("original=%#v err=%v", original, err)
	}
	// Simulate a persisted branch-generation change while retaining this exact
	// selected completion. A restarted runtime captures its current coordinates.
	if _, err := f.db.Exec(`UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=?`, f.stream.UID); err != nil {
		t.Fatal(err)
	}
	f.server.memoryConfig.ExtractMode = "forked"
	runtime = newMemoryExtractionRuntimeWithFactory(f.server, func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		t.Error("recaptured committed completion constructed another model")
		return nil
	})
	replayed, err := runtime.RunCompletedRoot(ctx, f.stream.SessionID, finish.EventID)
	if err != nil || !replayed.Replayed || replayed.Gate != original.Gate || replayed.CursorAfter != original.CursorAfter ||
		!reflect.DeepEqual(replayed.Applied, original.Applied) {
		t.Fatalf("recaptured replay=%#v original=%#v err=%v", replayed, original, err)
	}
}
