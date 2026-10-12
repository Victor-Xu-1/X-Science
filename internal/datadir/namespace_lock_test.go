package datadir

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const namespaceLockHelperEnv = "X_SCIENCE_POINTER_LOCK_CRASH_HELPER"

func TestNamespacePointerLockCrashHelper(t *testing.T) {
	path := os.Getenv(namespaceLockHelperEnv)
	if path == "" {
		return
	}
	guard, err := lockNamespacePointer(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = guard // Deliberately do not run Close/unlock or any deferred cleanup.
	os.Exit(23)
}

func TestNamespacePointerLockAbandonedTextAndProcessCrashDoNotBlockRecovery(t *testing.T) {
	previous, current := namespacePointerPaths(t)
	raw := writeNamespaceState(t, previous, State{SchemaVersion: 1, Current: filepath.Dir(previous)})
	if err := os.Mkdir(filepath.Dir(current), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(current), ".namespace-pointer-upgrade.lock")
	originalLock := []byte("default-pointer namespace upgrade\n")
	if err := os.WriteFile(path, originalLock, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestNamespacePointerLockCrashHelper$")
	command.Env = append(os.Environ(), namespaceLockHelperEnv+"="+path)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err == nil {
		t.Fatal("lock crash helper unexpectedly survived")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Fatalf("crash helper failed before acquiring its lock: %v; %s", err, output.String())
	}
	if err := UpgradeDefaultPointer(previous, current); err != nil {
		t.Fatalf("abandoned text or a dead owner prevented normal upgrade: %v", err)
	}
	actual, err := os.ReadFile(current)
	if err != nil || !bytes.Equal(raw, actual) {
		t.Fatal("recovered upgrade changed the original pointer")
	}
	lockBytes, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(originalLock, lockBytes) {
		t.Fatal("the durable lock inode was unlinked or its contents were altered")
	}
}

func TestNamespacePointerLockLiveContentionPreservesState(t *testing.T) {
	previous, current := namespacePointerPaths(t)
	raw := writeNamespaceState(t, previous, State{SchemaVersion: 1, Current: filepath.Dir(previous)})
	if err := os.Mkdir(filepath.Dir(current), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(current), ".namespace-pointer-upgrade.lock")
	guard, err := lockNamespacePointer(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpgradeDefaultPointer(previous, current); err == nil {
		releaseNamespaceLock(guard)
		t.Fatal("another live process lock was bypassed")
	}
	actual, err := os.ReadFile(previous)
	if err != nil || !bytes.Equal(raw, actual) {
		releaseNamespaceLock(guard)
		t.Fatal("contending upgrader changed original state")
	}
	if _, err := os.Lstat(current); !os.IsNotExist(err) {
		releaseNamespaceLock(guard)
		t.Fatal("contending upgrader published a second pointer")
	}
	releaseNamespaceLock(guard)
	for attempt := 0; attempt < 2; attempt++ {
		if err := UpgradeDefaultPointer(previous, current); err != nil {
			t.Fatalf("upgrade after lock release is not idempotent: %v", err)
		}
	}
}

func TestNamespacePointerLockRejectsAliasesWithoutDeletingThem(t *testing.T) {
	for _, alias := range []string{"symlink", "hardlink"} {
		t.Run(alias, func(t *testing.T) {
			previous, current := namespacePointerPaths(t)
			writeNamespaceState(t, previous, State{SchemaVersion: 1})
			if err := os.Mkdir(filepath.Dir(current), 0o700); err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(filepath.Dir(current), "foreign-lock-object")
			if err := os.WriteFile(original, []byte("do-not-modify"), 0o600); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(filepath.Dir(current), ".namespace-pointer-upgrade.lock")
			var err error
			if alias == "symlink" {
				err = os.Symlink(original, lock)
			} else {
				err = os.Link(original, lock)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := UpgradeDefaultPointer(previous, current); err == nil {
				t.Fatal("aliased lock was accepted")
			}
			if actual, err := os.ReadFile(original); err != nil || string(actual) != "do-not-modify" {
				t.Fatal("unsafe lock rejection changed another file")
			}
			if _, err := os.Lstat(lock); err != nil {
				t.Fatal("unsafe lock was blindly deleted")
			}
		})
	}
}
