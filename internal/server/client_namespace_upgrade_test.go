package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	settingsstore "x-science/internal/persistence/settings"
)

func TestWebClientNamespaceUpgradeOnlyOwnedKeys(t *testing.T) {
	store := settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
	prefix := webClientSettingsPrefix("owner")
	old := prefix + "synonBiomed.session.defaultModelId"
	if _, err := store.Set(old, "operator-model"); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{"user.synonBiomed.session.defaultModelId": "untouched", prefix + "language": "zh-CN"} {
		if _, err := store.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := upgradeWebClientSettingsNamespace(store, "owner"); err != nil {
		t.Fatal(err)
	}
	current, found, err := store.Get(prefix + "xScience.session.defaultModelId")
	if err != nil || !found || current.Value != "operator-model" {
		t.Fatal("owned server client-setting identity was not transferred")
	}
	if _, found, err := store.Get(old); err != nil || found {
		t.Fatal("superseded owned setting remains active")
	}
	for _, key := range []string{"user.synonBiomed.session.defaultModelId", prefix + "language"} {
		if _, found, err := store.Get(key); err != nil || !found {
			t.Fatal("neutral or user-owned setting changed")
		}
	}
}

func TestWebClientNamespaceConflictingForeignOwnerCannotBlockRead(t *testing.T) {
	store := settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
	first, foreign := webClientSettingsPrefix("first"), webClientSettingsPrefix("foreign")
	for key, value := range map[string]any{
		first + "synonBiomed.session.defaultModelId":   "first-model",
		foreign + "synonBiomed.session.defaultModelId": "older-foreign-model",
		foreign + "xScience.session.defaultModelId":    "different-foreign-model",
	} {
		if _, err := store.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	app := &Server{}
	app.settingsStore = store
	values, err := app.readWebClientSettings("first", nil)
	if err != nil || values["xScience.session.defaultModelId"] != "first-model" {
		t.Fatalf("unrelated owner blocked: values=%v err=%v", values, err)
	}
	for key, value := range map[string]any{foreign + "synonBiomed.session.defaultModelId": "older-foreign-model", foreign + "xScience.session.defaultModelId": "different-foreign-model"} {
		setting, found, err := store.Get(key)
		if err != nil || !found || setting.Value != value {
			t.Fatalf("foreign owner's conflict modified: %s", key)
		}
	}
	if _, err := app.readWebClientSettings("foreign", nil); err == nil {
		t.Fatal("conflicting same-owner authority silently chosen")
	}
}

func TestWebClientNamespacePUTRejectsRetiredKeysBeforeAnyWrite(t *testing.T) {
	app := New(Options{FileRoot: t.TempDir()})
	request := httptest.NewRequest(http.MethodPut, "/api/settings/client", strings.NewReader(`{"language":"zh-CN","synonBiomed.session.defaultModelId":"obsolete"}`))
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("retired write accepted: %d %s", response.Code, response.Body.String())
	}
	stored, err := app.settingsStore.List()
	if err != nil {
		t.Fatal(err)
	}
	for key := range stored {
		if strings.HasPrefix(key, "web.client.") {
			t.Fatalf("rejected request partially wrote %s", key)
		}
	}
}
