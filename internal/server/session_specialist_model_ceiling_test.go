package server

import (
	"errors"
	"net/http"
	"testing"

	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

func TestSessionSpecialistFrozenModelsDoNotExpandWithCatalog(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "captured-model", true: "captured-empty"}[empty], func(t *testing.T) {
			app, store, frame := newSessionSpecialistAPIFixture(t)
			register := func(id, model string) {
				enabled := true
				if _, err := store.RegisterModelProvider(workspace.ModelProviderInput{ID: id, UserID: specialistFixtureOwner,
					Name: id, Type: "openai-compatible", BaseURL: "https://models.example.test/v1", Model: model, Enabled: &enabled}); err != nil {
					t.Fatal(err)
				}
			}
			if !empty {
				register("captured-provider", "synthetic-model-a")
			}
			prepared := specialistPrepareFixture(t, app, frame.ID, "SYNTHETIC_A", "model-ceiling-command", false)
			reference := sessionagent.InputReference{Scope: prepared.Snapshot.Scope, Branch: prepared.Snapshot.PreparedBranch,
				Revision: prepared.Snapshot.Revision, SnapshotID: prepared.Snapshot.Desired.SnapshotID}
			definition, err := store.SessionSpecialistDefinition(t.Context(), specialistFixtureOwner, frame.ID, &reference)
			if err != nil || definition == nil {
				t.Fatalf("definition=%#v err=%v", definition, err)
			}
			if empty && len(definition.Settings.AllowedModels) != 0 {
				t.Fatalf("empty fixture has models: %#v", definition.Settings.AllowedModels)
			}
			compat, found, err := store.GetCompatibilityFrame(frame.ID)
			if err != nil || !found {
				t.Fatalf("frame found=%t err=%v", found, err)
			}
			if !empty {
				input := &webConversationSendInput{SessionOptions: map[string]any{sessionagent.InputReferenceKey: reference, "model": "synthetic-model-a"}}
				if err := app.validateWebConversationSendInput(specialistFixtureOwner, compat, input); err != nil {
					t.Fatalf("captured model denied: %v", err)
				}
			}
			register("later-provider", "synthetic-model-b")
			frozen := frozenSpecialistAssistant(*definition)
			models, err := app.webAssistantAllowedModels(specialistFixtureOwner, frozen)
			if err != nil {
				t.Fatal(err)
			}
			if _, widened := normalizedSkillNameSet(models)["synthetic-model-b"]; widened {
				t.Errorf("a later catalog entry widened the frozen model maximum: %#v", models)
			}
			for _, key := range []string{"model", "subagent_model"} {
				input := &webConversationSendInput{SessionOptions: map[string]any{sessionagent.InputReferenceKey: reference, key: "synthetic-model-b"}}
				err := app.validateWebConversationSendInput(specialistFixtureOwner, compat, input)
				var rejection *webConversationRequestError
				if !errors.As(err, &rejection) || rejection.Status != http.StatusForbidden {
					t.Errorf("%s escaped frozen admission: %v", key, err)
				}
			}
			if _, err := app.normalizeWebAssistantConversationOverrides(specialistFixtureOwner, frozen, map[string]any{"model": "synthetic-model-b"}); err == nil {
				t.Error("the override consumer re-expanded the same frozen record")
			}
			legacy, found, err := app.webAssistantRecord(specialistFixtureOwner, "SYNTHETIC_A")
			if err != nil || !found {
				t.Fatalf("legacy record found=%t err=%v", found, err)
			}
			live, err := app.webAssistantAllowedModels(specialistFixtureOwner, legacy)
			if err != nil {
				t.Fatal(err)
			}
			if _, allowed := normalizedSkillNameSet(live)["synthetic-model-b"]; !allowed {
				t.Fatal("legacy live model catalog semantics were removed")
			}
		})
	}
}
