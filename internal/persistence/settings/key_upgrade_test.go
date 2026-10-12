package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRenameKeysRetainsValuesTimeAndIsIdempotent(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "settings.json"))
	before, err := store.Set("previous", map[string]any{"text": "user bytes", "order": []any{"file", "project"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RenameKeys(map[string]string{"previous": "current"}); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Get("current")
	if err != nil || !found || got.Key != "current" || !got.UpdatedAt.Equal(before.UpdatedAt) || !reflect.DeepEqual(got.Value, before.Value) {
		t.Fatal("settings identity transfer changed its value or timestamp")
	}
	raw, _ := os.ReadFile(store.path)
	if err := store.RenameKeys(map[string]string{"previous": "current"}); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(store.path)
	if string(raw) != string(again) {
		t.Fatal("settings upgrade was not byte-idempotent")
	}
}

func TestRenameKeysConflictAbortsEntireTransfer(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "settings.json"))
	for key, value := range map[string]string{"a": "first", "b": "second", "current-b": "other"} {
		if _, err := store.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.ReadFile(store.path)
	if err := store.RenameKeys(map[string]string{"a": "current-a", "b": "current-b"}); err == nil {
		t.Fatal("conflicting authority accepted")
	}
	after, _ := os.ReadFile(store.path)
	if string(before) != string(after) {
		t.Fatal("partial settings transfer was committed")
	}
	for _, plan := range []map[string]string{{"a": "b", "b": "a"}, {"a": "same", "b": "same"}, {"a": "../path"}} {
		if err := store.RenameKeys(plan); err == nil {
			t.Fatal("ambiguous settings upgrade plan accepted")
		}
	}
}
