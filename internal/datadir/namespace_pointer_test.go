package datadir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func namespacePointerPaths(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	previous := filepath.Join(root, "synon-go", "data-dir.json")
	current := filepath.Join(root, "x-science", "data-dir.json")
	if err := os.Mkdir(filepath.Dir(previous), 0o700); err != nil {
		t.Fatal(err)
	}
	return previous, current
}

func writeNamespaceState(t *testing.T, path string, state State) []byte {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNamespaceDefaultPointerPreservesOriginalBytesAndOneAuthority(t *testing.T) {
	previous, current := namespacePointerPaths(t)
	root := filepath.Dir(filepath.Dir(current))
	data := filepath.Join(root, "chosen-data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	state := State{SchemaVersion: 1, Current: data, Activation: &Activation{
		ID: "operator-activation", ActivationKey: "opaque-id", Target: data,
		PreviousCurrent: filepath.Join(root, "previous-location"), ActivatedAt: time.Unix(100, 0).UTC(),
	}}
	raw := writeNamespaceState(t, previous, state)
	// Unknown future fields and formatting are original source facts, not a
	// reason to rewrite a user's activation record during namespace transfer.
	raw = append(raw[:len(raw)-1], []byte(",\"futureOwnerReceipt\":{\"id\":\"unchanged\"}}\n")...)
	if err := os.WriteFile(previous, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpgradeDefaultPointer(previous, current); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(current)
	if err != nil || !bytes.Equal(raw, actual) {
		t.Fatalf("original pointer bytes changed: %v", err)
	}
	if _, err := os.Lstat(previous); !os.IsNotExist(err) {
		t.Fatal("previous pointer remained a competing authority")
	}
	recovery, err := filepath.Glob(filepath.Join(filepath.Dir(current), ".namespace-pointer-source-*.json"))
	if err != nil || len(recovery) != 1 {
		t.Fatalf("recovery snapshots = %v, err=%v", recovery, err)
	}
	backup, err := os.ReadFile(recovery[0])
	if err != nil || !bytes.Equal(raw, backup) {
		t.Fatal("hash-bound original recovery bytes were not retained")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := UpgradeDefaultPointer(previous, current); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := New(current).Load()
	if err != nil || !reflect.DeepEqual(state, loaded) {
		t.Fatalf("move/activation semantics changed: %+v, err=%v", loaded, err)
	}
	// Normal future updates use only the canonical controller. Recovery stays
	// immutable and is never treated as another writable pointer.
	if _, err := New(current).RollbackActivation(state.Activation.ID); err == nil {
		t.Fatal("unavailable previous location was silently accepted")
	}
	state.Activation = nil
	if err := New(current).saveLocked(state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(previous); !os.IsNotExist(err) {
		t.Fatal("future writes recreated the original pointer")
	}
	backup, _ = os.ReadFile(recovery[0])
	if !bytes.Equal(raw, backup) {
		t.Fatal("future writes modified recovery")
	}
}

func TestNamespaceDefaultPointerFailsBeforePublication(t *testing.T) {
	for _, scenario := range []string{"conflict", "same-bytes-conflict", "unsupported-schema", "source-symlink", "source-hardlink", "target-symlink", "active-guard", "recovery-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			previous, current := namespacePointerPaths(t)
			state := State{SchemaVersion: 1, Current: filepath.Dir(previous)}
			raw := writeNamespaceState(t, previous, state)
			if err := os.MkdirAll(filepath.Dir(current), 0o700); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "conflict":
				writeNamespaceState(t, current, State{SchemaVersion: 1, Current: filepath.Dir(current)})
			case "same-bytes-conflict":
				writeNamespaceState(t, current, state)
			case "unsupported-schema":
				raw = writeNamespaceState(t, previous, State{SchemaVersion: 99})
			case "source-symlink":
				original := previous + ".original"
				if err := os.Rename(previous, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, previous); err != nil {
					t.Fatal(err)
				}
			case "source-hardlink":
				if err := os.Link(previous, previous+".alias"); err != nil {
					t.Fatal(err)
				}
			case "target-symlink":
				if err := os.Symlink(previous, current); err != nil {
					t.Fatal(err)
				}
			case "active-guard":
				guard, err := lockNamespacePointer(filepath.Join(filepath.Dir(current), ".namespace-pointer-upgrade.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer releaseNamespaceLock(guard)
			case "recovery-conflict":
				snapshot, err := namespaceFile(previous)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(current), fmt.Sprintf(".namespace-pointer-source-%x.json", snapshot.hash)), []byte("foreign-original"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, targetErr := os.ReadFile(current)
			if err := UpgradeDefaultPointer(previous, current); err == nil {
				t.Fatal("unsafe original pointer was accepted")
			}
			after, err := os.ReadFile(previous)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatalf("original source was lost or changed: %v", err)
			}
			if targetErr == nil {
				actual, err := os.ReadFile(current)
				if err != nil || !bytes.Equal(before, actual) {
					t.Fatal("existing canonical pointer was overwritten")
				}
			} else if _, err := os.Lstat(current); !os.IsNotExist(err) {
				t.Fatal("invalid input was published as a canonical pointer")
			}
		})
	}
}

func TestNamespaceDefaultPointerRejectsCustomOrTraversingPaths(t *testing.T) {
	previous, current := namespacePointerPaths(t)
	writeNamespaceState(t, previous, State{SchemaVersion: 1})
	for _, target := range []string{current + ".custom", filepath.Join(filepath.Dir(current), "nested", "data-dir.json"), "relative/data-dir.json"} {
		if err := UpgradeDefaultPointer(previous, target); err == nil {
			t.Fatal("non-default pointer was modified")
		}
	}
}
