//go:build windows

package runtimecontrol

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsAvailableBytesExistingFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"plain", "研究-容量-😀"} {
		directory := filepath.Join(root, name)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(directory, "artifact.bin")
		if err := os.WriteFile(file, []byte("existing stage"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, target := range []struct{ kind, path string }{{"directory", directory}, {"file", file}} {
			t.Run(name+"/"+target.kind, func(t *testing.T) {
				if capacity := AvailableBytes(target.path); capacity == nil || *capacity == 0 {
					t.Fatalf("existing %s returned no measured capacity: %v", target.kind, capacity)
				}
			})
		}
	}
}

func TestWindowsAvailableBytesUsesResolvedLinkDestination(t *testing.T) {
	root := t.TempDir()
	targetDirectory := filepath.Join(root, "actual-目标-😀")
	linkDirectory := filepath.Join(root, "links")
	for _, directory := range []string{targetDirectory, linkDirectory} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(targetDirectory, "stage.bin")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertDestination := func(t *testing.T, path string) {
		t.Helper()
		directory, err := windowsCapacityDirectory(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.Stat(directory)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.Stat(targetDirectory)
		if err != nil || !os.SameFile(got, want) {
			t.Fatalf("capacity directory is not the actual link target: %q err=%v", directory, err)
		}
		if capacity := AvailableBytes(path); capacity == nil || *capacity == 0 {
			t.Fatalf("resolved target has no measured capacity: %v", capacity)
		}
	}
	t.Run("file-symlink", func(t *testing.T) {
		link := filepath.Join(linkDirectory, "linked-stage.bin")
		if err := os.Symlink(target, link); err != nil {
			if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
				t.Skipf("host lacks file-symlink creation privilege: %v", err)
			}
			t.Fatalf("create owned temporary file symlink: %v", err)
		}
		assertDestination(t, link)
		dangling := filepath.Join(linkDirectory, "dangling.bin")
		if err := os.Symlink(filepath.Join(root, "missing.bin"), dangling); err != nil {
			t.Fatal(err)
		}
		if AvailableBytes(dangling) != nil {
			t.Fatal("dangling file link borrowed capacity from its own directory")
		}
	})
	t.Run("directory-junction", func(t *testing.T) {
		link := filepath.Join(linkDirectory, "junction")
		if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, targetDirectory).CombinedOutput(); err != nil {
			t.Fatalf("create owned temporary junction: %v: %s", err, output)
		}
		if destination, err := os.Readlink(link); err != nil {
			t.Fatalf("read actual junction target: %v", err)
		} else if destination != targetDirectory {
			t.Fatalf("junction fixture targets %q instead of %q", destination, targetDirectory)
		}
		assertDestination(t, link)
		assertDestination(t, filepath.Join(link, "stage.bin"))
	})
}

func TestWindowsAvailableBytesUnknownPathStaysUnavailable(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"", filepath.Join(root, "missing"), filepath.Join(root, "missing", "child"), "invalid\x00path"} {
		if got := AvailableBytes(path); got != nil {
			t.Fatalf("unknown/invalid path %q borrowed unrelated capacity: %d", path, *got)
		}
	}
}

func TestWindowsDiskCapacityWriterUsesExistingDestinationFile(t *testing.T) {
	directory := t.TempDir()
	const content = "bounded artifact chunk"
	capacity := AvailableBytes(directory)
	if capacity == nil {
		t.Fatal("temporary directory capacity is unavailable")
	}
	if *capacity < DiskFreeReserve+uint64(len(content)) {
		t.Skip("actual temporary volume has insufficient headroom for the unchanged reserve")
	}
	file, err := os.Create(filepath.Join(directory, "实际-stage.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	written, err := DiskCapacityWriter(context.Background(), file, file.Name()).Write([]byte(content))
	if err != nil || written != len(content) {
		t.Fatalf("actual file writer rejected measured headroom: bytes=%d err=%v", written, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(file.Name()); err != nil || string(got) != content {
		t.Fatalf("actual stage contents=%q err=%v", got, err)
	}
}
