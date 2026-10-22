package server

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	settingsstore "x-science/internal/persistence/settings"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestAutomaticMemoryCommitDoesNotInvertSkillImportSettingsLock(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	f.server.settingsStore = settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
	f.server.memoryConfig.ExtractEnabled = true
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	databaseHeld, allowMetadata, memoryReady := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseMetadata := sync.OnceFunc(func() { close(allowMetadata) })
	defer releaseMetadata()
	importDone, memoryDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := f.repo.RunImmediateWithOutcome(ctx, func(*transcriptstore.ImmediateTransaction) error {
			close(databaseHeld)
			select {
			case <-allowMetadata:
			case <-ctx.Done():
				return ctx.Err()
			}
			// The real Skill importer persists these unrelated settings while
			// holding its existing SQLite mutation commit guard.
			return f.server.recordSkillImports(f.stream.OwnerID, nil, []webSkillImportHistoryEntry{{
				ID: "lock-order-import", OperationID: "lock-order-operation", SkillName: "generic-method", Status: "imported",
			}})
		})
		importDone <- err
	}()
	select {
	case <-databaseHeld:
	case err := <-importDone:
		t.Fatalf("import did not acquire its database fence: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		memoryDone <- f.server.withAutomaticMemoryCommit(ctx, f.stream.OwnerID, func() error {
			close(memoryReady)
			_, err := f.repo.RunImmediateWithOutcome(ctx, func(*transcriptstore.ImmediateTransaction) error { return nil })
			return err
		})
	}()
	select {
	case <-memoryReady:
	case err := <-memoryDone:
		t.Fatalf("memory did not reach its commit boundary: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	releaseMetadata()
	for _, done := range []<-chan error{importDone, memoryDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("ordinary Skill import and Memory commit conflicted: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("settings/SQLite lock cycle prevented completion")
		}
	}
	if err := f.server.setMemoryAutoExtractionEnabled(f.stream.OwnerID, false); err != nil {
		t.Fatal(err)
	}
	if err := f.server.withAutomaticMemoryCommit(ctx, f.stream.OwnerID, func() error {
		t.Error("disabled automatic Memory ran a new commit")
		return nil
	}); err == nil {
		t.Fatal("disabled automatic Memory accepted a fresh commit")
	}
}

func TestAutomaticMemoryPreferenceRejectsGenericSettingBypass(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	f.server.settingsStore = settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
	for _, key := range []string{
		memoryAutoExtractionSettingKey,
		userPreferenceSettingKey(memoryAutoExtractionSettingKey, f.stream.OwnerID),
		memoryAutoExtractionSettingKey + ".user.invalid-raw-owner",
	} {
		if _, err := f.server.executeSettingsTool("settings_set", map[string]any{"key": key, "value": false}); err == nil || !strings.Contains(err.Error(), "memory preference API") {
			t.Fatalf("generic automatic preference bypass key=%q err=%v", key, err)
		}
		if _, found, err := f.server.settingsStore.Get(key); err != nil || found {
			t.Fatalf("rejected preference persisted key=%q found=%t err=%v", key, found, err)
		}
	}
	if err := f.server.setMemoryAutoExtractionEnabled(f.stream.OwnerID, false); err != nil {
		t.Fatal(err)
	}
	if enabled, err := f.server.memoryAutoExtractionEnabled(f.stream.OwnerID); err != nil || enabled {
		t.Fatalf("owner preference path=%t err=%v", enabled, err)
	}
	if _, err := f.server.executeSettingsTool("settings_set", map[string]any{"key": "theme", "value": "dark"}); err != nil {
		t.Fatalf("unrelated generic setting changed: %v", err)
	}
}
