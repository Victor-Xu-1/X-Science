//go:build windows

package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWorkspaceMinimalParentSupportsRelativeCreation(t *testing.T) {
	owned := t.TempDir()
	traverse := uint32(windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	writable := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.DELETE | windows.SYNCHRONIZE)
	parent, err := openAgentWorkspaceWindowsRoot(owned, traverse)
	if err != nil {
		t.Fatalf("owned parent traversal handle unavailable: %T", err)
	}
	defer windows.CloseHandle(parent)
	child, err := openAgentWorkspaceWindowsObject(parent, "one", traverse, windows.FILE_OPEN_IF, windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if err != nil {
		t.Fatalf("minimal parent did not permit authorized relative directory creation: %T", err)
	}
	defer windows.CloseHandle(child)
	if err := validateAgentWorkspaceWindowsDirectory(child); err != nil {
		t.Fatalf("created child is not a validated directory: %T", err)
	}
	leaf, err := openAgentWorkspaceWindowsObject(child, "two", writable, windows.FILE_OPEN_IF, windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if err != nil {
		t.Fatalf("minimal parent did not permit writable final descendant creation: %T", err)
	}
	defer windows.CloseHandle(leaf)
	if err := validateAgentWorkspaceWindowsDirectory(leaf); err != nil {
		t.Fatalf("final created leaf is not a validated directory: %T", err)
	}
	info, err := os.Stat(filepath.Join(owned, "one", "two"))
	if err != nil || !info.IsDir() {
		t.Fatal("relative creation did not create the owned two-level descendant")
	}
	t.Log("minimal parent handle authorized relative multi-level creation; final handle retained original write/delete mask; all paths are owned fixture")
}
func TestWorkspaceEnsureOwnedDirectories(t *testing.T) {
	owned := t.TempDir()
	for _, target := range []string{owned, filepath.Join(owned, "one", "two", "three")} {
		got, err := canonicalOrCreateAgentWorkspaceDirectory(target, 0o700)
		if err != nil {
			t.Fatalf("owned existing/missing descendant admission failed: %T", err)
		}
		if !strings.EqualFold(filepath.Clean(got), filepath.Clean(target)) {
			t.Fatal("held-handle readback differs from the owned canonical target")
		}
		handle, err := openAgentWorkspaceWindowsRoot(got, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE)
		if err != nil {
			t.Fatalf("admitted final target lacks original write/delete access: %T", err)
		}
		_ = windows.CloseHandle(handle)
	}
}

func TestWorkspaceRootTargetKeepsWriteDeleteAdmission(t *testing.T) {
	owned := t.TempDir()
	root := filepath.VolumeName(owned) + string(os.PathSeparator)
	handle, wantError := openAgentWorkspaceWindowsRoot(root, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE)
	if wantError == nil {
		_ = windows.CloseHandle(handle)
	}
	_, err := secureEnsureAgentWorkspaceDirectory(root, 0o700)
	t.Logf("real root-final write/delete denial observed=%t; no ACL was changed", errors.Is(wantError, windows.STATUS_ACCESS_DENIED))
	if wantError != nil {
		if !errors.Is(err, wantError) {
			t.Fatalf("readable root was admitted without its original final write/delete rights: got=%T want=%T", err, wantError)
		}
	} else if err != nil {
		t.Fatalf("OS-authorized writable root target was rejected: %T", err)
	}
}
func TestWorkspaceFinalTargetKeepsWriteAndDeleteAccess(t *testing.T) {
	for _, test := range []struct {
		name  string
		share uint32
	}{
		{"write", windows.FILE_SHARE_READ | windows.FILE_SHARE_DELETE},
		{"delete", windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := t.TempDir()
			name, err := windows.UTF16PtrFromString(target)
			if err != nil {
				t.Fatal("fixture path encoding failed")
			}
			handle, err := windows.CreateFile(name, windows.FILE_GENERIC_READ|windows.SYNCHRONIZE, test.share, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if err != nil {
				t.Fatalf("fixture sharing guard unavailable: %T", err)
			}
			defer windows.CloseHandle(handle)
			probe, probeErr := openAgentWorkspaceWindowsRoot(target, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE)
			if probeErr == nil {
				_ = windows.CloseHandle(probe)
			}
			if !errors.Is(probeErr, windows.STATUS_SHARING_VIOLATION) {
				t.Fatalf("negative fixture did not deny the original final mask: %T", probeErr)
			}
			_, err = secureEnsureAgentWorkspaceDirectory(target, 0o700)
			if !errors.Is(err, windows.STATUS_SHARING_VIOLATION) {
				t.Fatalf("final target lost its original %s access requirement: %T", test.name, err)
			}
		})
	}
}
