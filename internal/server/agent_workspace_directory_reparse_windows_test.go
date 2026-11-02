//go:build windows

package server

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func createWorkspaceAdmissionJunction(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Mkdir(link, 0o700); err != nil {
		t.Fatalf("owned junction directory setup failed: %T", err)
	}
	name, err := windows.UTF16PtrFromString(link)
	if err != nil {
		t.Fatal("owned junction path encoding failed")
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatalf("owned junction handle unavailable: %T", err)
	}
	defer windows.CloseHandle(handle)
	substitute, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		t.Fatal("owned junction substitute encoding failed")
	}
	printed, err := windows.UTF16FromString(target)
	if err != nil {
		t.Fatal("owned junction print-name encoding failed")
	}
	pathBytes := 2 * (len(substitute) + len(printed))
	if pathBytes+8 > 65535 {
		t.Fatal("junction fixture path exceeds the native buffer contract")
	}
	data := make([]byte, 16+pathBytes)
	binary.LittleEndian.PutUint32(data[0:4], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(data[4:6], uint16(pathBytes+8))
	binary.LittleEndian.PutUint16(data[10:12], uint16(2*(len(substitute)-1)))
	binary.LittleEndian.PutUint16(data[12:14], uint16(2*len(substitute)))
	binary.LittleEndian.PutUint16(data[14:16], uint16(2*(len(printed)-1)))
	for i, value := range append(substitute, printed...) {
		binary.LittleEndian.PutUint16(data[16+2*i:], value)
	}
	var returned uint32
	if err := windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &data[0], uint32(len(data)), nil, 0, &returned, nil); err != nil {
		t.Fatalf("real junction creation unavailable under current token; reparse coverage not claimed: %T", err)
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		t.Fatal("fixture did not create a real native reparse point")
	}
	t.Cleanup(func() {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			t.Errorf("owned junction removal failed: %T", err)
		}
	})
	t.Log("real mount-point junction created under current token; no ACL or privilege settings changed")
}

func TestWorkspaceDirectoryAdmissionRejectsRealReparse(t *testing.T) {
	owned := t.TempDir()
	target := filepath.Join(owned, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal("owned reparse target setup failed")
	}
	junction := filepath.Join(owned, "junction")
	createWorkspaceAdmissionJunction(t, junction, target)
	for _, path := range []string{junction, filepath.Join(junction, "must-not-be-created")} {
		_, err := secureEnsureAgentWorkspaceDirectory(path, 0o700)
		if !errors.Is(err, windows.STATUS_REPARSE_POINT_ENCOUNTERED) {
			t.Fatalf("real reparse traversal was not rejected by its native guard: %T", err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "must-not-be-created")); !os.IsNotExist(err) {
		t.Fatal("admission followed a junction to create a descendant")
	}
}

func TestWorkspaceDirectoryCreationRaceUsesHeldParent(t *testing.T) {
	for _, reparse := range []bool{false, true} {
		name := "directory"
		if reparse {
			name = "junction"
		}
		t.Run(name, func(t *testing.T) {
			owned := t.TempDir()
			parent, err := openAgentWorkspaceWindowsRoot(owned, windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE)
			if err != nil {
				t.Fatalf("owned held parent unavailable: %T", err)
			}
			defer windows.CloseHandle(parent)
			access := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.DELETE | windows.SYNCHRONIZE)
			handle, err := openAgentWorkspaceWindowsObject(parent, "raced", access, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT)
			if err == nil {
				windows.CloseHandle(handle)
				t.Fatal("race fixture unexpectedly existed before its creator")
			}
			if !errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) {
				t.Fatalf("initial native open was not a genuine missing child: %T", err)
			}
			if reparse {
				target := filepath.Join(owned, "target")
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal("owned target setup failed")
				}
				createWorkspaceAdmissionJunction(t, filepath.Join(owned, "raced"), target)
			} else if err := os.Mkdir(filepath.Join(owned, "raced"), 0o700); err != nil {
				t.Fatal("owned concurrent creator setup failed")
			}
			handle, err = openAgentWorkspaceWindowsObject(parent, "raced", access, windows.FILE_OPEN_IF, windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT)
			if reparse {
				if !errors.Is(err, windows.STATUS_REPARSE_POINT_ENCOUNTERED) {
					if err == nil {
						windows.CloseHandle(handle)
					}
					t.Fatalf("post-missing creation followed a raced real junction: %T", err)
				}
			} else {
				if err != nil {
					t.Fatalf("benign concurrent creation was not opened under the original parent: %T", err)
				}
				defer windows.CloseHandle(handle)
				if err := validateAgentWorkspaceWindowsDirectory(handle); err != nil {
					t.Fatal("concurrently created directory validation failed")
				}
			}
		})
	}
}
