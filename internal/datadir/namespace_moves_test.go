package datadir

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func originalNamespaceMove(t *testing.T) (string, string, Move) {
	t.Helper()
	previous, current := namespacePointerPaths(t)
	root := filepath.Dir(filepath.Dir(previous))
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	for _, path := range []string{source, target} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	move := Move{ID: "recorded-move-id", Source: source, Target: target, Migrate: true,
		InventoryCaptured: true, Entries: []string{"durable.bin"}, StagedAt: time.Unix(50, 0).UTC()}
	if err := os.WriteFile(filepath.Join(source, previousSourceMarkerPrefix+move.ID), []byte(move.ID), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "durable.bin"), []byte("scientific data remains opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	return previous, current, move
}

func TestNamespacePendingMoveAndCleanupKeepReceiptOwnership(t *testing.T) {
	previous, current, move := originalNamespaceMove(t)
	raw := writeNamespaceState(t, previous, State{SchemaVersion: 1, Current: move.Source, Pending: &move})
	if err := UpgradeDefaultPointer(previous, current); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(current)
	if !bytes.Equal(raw, actual) {
		t.Fatal("pending move receipt was rewritten during transfer")
	}
	loaded, err := New(current).Load()
	if err != nil || !reflect.DeepEqual(&move, loaded.Pending) {
		t.Fatalf("pending receipt changed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(move.Source, previousSourceMarkerPrefix+move.ID)); !os.IsNotExist(err) {
		t.Fatal("old source marker remained active")
	}
	if target, _, err := New(current).Resolve(move.Source); err != nil || target != move.Target {
		t.Fatalf("upgraded move did not finish: %v", err)
	}
	actual, err = os.ReadFile(filepath.Join(move.Target, "durable.bin"))
	if err != nil || string(actual) != "scientific data remains opaque" {
		t.Fatal("durable move data changed")
	}
	if err := os.WriteFile(filepath.Join(move.Source, "created-later"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New(current).ClearLastMove(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(move.Source, "created-later")); err != nil {
		t.Fatal("cleanup deleted data outside the original inventory")
	}
}

func TestNamespaceCompletedStagingResumesWithoutRecopy(t *testing.T) {
	previous, current, move := originalNamespaceMove(t)
	staging := move.Target + previousStagingInfix + move.ID
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, previousCompletionMarker), []byte(move.ID), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "durable.bin"), []byte("already verified original copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeNamespaceState(t, previous, State{SchemaVersion: 1, Current: move.Source, Pending: &move})
	if err := UpgradeDefaultPointer(previous, current); err != nil {
		t.Fatal(err)
	}
	if _, _, err := New(current).Resolve(move.Source); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(filepath.Join(move.Target, "durable.bin"))
	if string(actual) != "already verified original copy" {
		t.Fatal("completed staging was discarded and source recopied")
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatal("original staging remained as a competing copy")
	}
}

func TestNamespaceLastMoveCompletionKeepsOriginalReceipt(t *testing.T) {
	previous, current, move := originalNamespaceMove(t)
	completed := time.Unix(100, 0).UTC()
	move.Copied = true
	move.CompletedAt = &completed
	if err := os.WriteFile(filepath.Join(move.Target, previousCompletionMarker), []byte(move.ID), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := writeNamespaceState(t, previous, State{SchemaVersion: 1, Current: move.Target, LastMove: &move})
	if err := UpgradeDefaultPointer(previous, current); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(current)
	loaded, err := New(current).Load()
	if err != nil || !bytes.Equal(raw, actual) || !reflect.DeepEqual(&move, loaded.LastMove) {
		t.Fatalf("completed move receipt changed: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(move.Target, completionMarker))
	if err != nil || string(marker) != move.ID {
		t.Fatal("completion receipt did not transfer to its canonical name")
	}
	if err := New(current).ClearLastMove(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(move.Source, "durable.bin")); err != nil {
		t.Fatal("non-deleting cleanup removed original data")
	}
}

func TestNamespaceMoveRejectsUnprovenOrConflictingArtifactsBeforeChanges(t *testing.T) {
	for _, scenario := range []string{"marker-conflict", "wrong-source-id", "partial-staging", "wrong-completion-id", "staging-symlink", "unrelated-staging", "traversing-id"} {
		t.Run(scenario, func(t *testing.T) {
			previous, current, move := originalNamespaceMove(t)
			oldMarker := filepath.Join(move.Source, previousSourceMarkerPrefix+move.ID)
			switch scenario {
			case "marker-conflict":
				if err := os.WriteFile(sourceMarkerPath(move.Source, move.ID), []byte(move.ID), 0o600); err != nil {
					t.Fatal(err)
				}
			case "wrong-source-id":
				if err := os.WriteFile(oldMarker, []byte("another-move"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "partial-staging", "wrong-completion-id", "staging-symlink", "unrelated-staging":
				staging := move.Target + previousStagingInfix + move.ID
				if err := os.Mkdir(staging, 0o700); err != nil {
					t.Fatal(err)
				}
				if scenario != "partial-staging" {
					id := move.ID
					if scenario == "wrong-completion-id" {
						id = "another-move"
					}
					if err := os.WriteFile(filepath.Join(staging, previousCompletionMarker), []byte(id), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "staging-symlink" {
					if err := os.Symlink(move.Source, filepath.Join(staging, "alias")); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "unrelated-staging" {
					if err := os.Remove(oldMarker); err != nil {
						t.Fatal(err)
					}
				}
			case "traversing-id":
				move.ID = "../../unrelated"
			}
			raw := writeNamespaceState(t, previous, State{SchemaVersion: 1, Current: move.Source, Pending: &move})
			if err := UpgradeDefaultPointer(previous, current); err == nil {
				t.Fatal("unsafe move namespace was accepted")
			}
			actual, _ := os.ReadFile(previous)
			if !bytes.Equal(raw, actual) {
				t.Fatal("invalid move changed the original pointer")
			}
			if _, err := os.Lstat(current); !os.IsNotExist(err) {
				t.Fatal("invalid move published a new pointer")
			}
			if scenario != "unrelated-staging" {
				if _, err := os.Lstat(oldMarker); err != nil {
					t.Fatal("preflight failure changed the original source marker")
				}
			}
		})
	}
}

func TestNamespaceCustomPointerKeepsItsPathAndUpgradesOnlyBoundMarkers(t *testing.T) {
	previous, _, move := originalNamespaceMove(t)
	custom := filepath.Join(filepath.Dir(previous), "operator-chosen.json")
	writeNamespaceState(t, custom, State{SchemaVersion: 1, Current: move.Source, Pending: &move})
	loaded, err := New(custom).Load()
	if err != nil || loaded.Pending.ID != move.ID || New(custom).Path() != custom {
		t.Fatalf("operator pointer or receipt changed: %v", err)
	}
	if _, err := os.Lstat(previous); !os.IsNotExist(err) {
		t.Fatal("custom pointer wrote another default authority")
	}
}
