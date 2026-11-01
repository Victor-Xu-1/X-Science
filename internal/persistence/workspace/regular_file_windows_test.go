//go:build windows

package workspace

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRegularFileReaderWindowsSupportsLongUnicodeLocalPath(t *testing.T) {
	directory := t.TempDir()
	for len([]rune(directory)) < 300 {
		directory = filepath.Join(directory, "长目录-segment")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "分子数据-😀.bin")
	payload := []byte("Unicode content survives an open-reader delete")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := openRegularFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	prefix := make([]byte, 3)
	if _, err := io.ReadFull(reader, prefix); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(append(prefix, rest...), payload) {
		t.Fatalf("remaining read: %q err=%v", rest, err)
	}
}

func TestRegularFileReaderWindowsPreservesReadOnlyAndIdentityChecks(t *testing.T) {
	directory := t.TempDir()
	path, other := filepath.Join(directory, "original"), filepath.Join(directory, "different")
	for _, name := range []string{path, other} {
		if err := os.WriteFile(name, []byte("unchanged"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := openRegularFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Write([]byte("forbidden")); err == nil {
		t.Fatal("read handle accepted a write")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	otherInfo, err := os.Lstat(other)
	if err != nil {
		t.Fatal(err)
	}
	if file, err := openRegularFileReader(path, otherInfo); err == nil || file != nil {
		if file != nil {
			_ = file.Close()
		}
		t.Fatal("different file identity was accepted")
	}
	if file, err := openRegularFile(directory); err == nil || file != nil {
		if file != nil {
			_ = file.Close()
		}
		t.Fatal("directory accepted as regular content")
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != "unchanged" {
		t.Fatalf("read changed content: %q err=%v", actual, err)
	}
}

func TestRegularFileReaderWindowsPathForms(t *testing.T) {
	for _, test := range []struct{ name, want string }{
		{`C:\目录\reader.bin`, `\\?\C:\目录\reader.bin`},
		{`\\server\share\目录\reader.bin`, `\\?\UNC\server\share\目录\reader.bin`},
		{`\\?\C:\目录\reader.bin`, `\\?\C:\目录\reader.bin`},
		{`\\?\UNC\server\share\目录\reader.bin`, `\\?\UNC\server\share\目录\reader.bin`},
	} {
		got, err := regularFileWindowsPath(test.name)
		if err != nil || got != test.want {
			t.Fatalf("%q => %q, want %q err=%v", test.name, got, test.want, err)
		}
	}
	relative := filepath.Join("relative", "reader.bin")
	absolute, err := filepath.Abs(relative)
	if err != nil {
		t.Fatal(err)
	}
	got, err := regularFileWindowsPath(relative)
	if err != nil || !strings.HasSuffix(got, absolute) || !strings.HasPrefix(got, `\\?\`) {
		t.Fatalf("relative path: %q err=%v", got, err)
	}
}

func TestRegularFileReaderWindowsRefusesSymlinkHandle(t *testing.T) {
	directory := t.TempDir()
	target, link := filepath.Join(directory, "target"), filepath.Join(directory, "link")
	if err := os.WriteFile(target, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
			t.Skip("Windows symlink fixture requires unavailable privilege (1314)")
		}
		t.Fatal(err)
	}
	expected, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if file, err := openRegularFileReader(link, expected); err == nil || file != nil {
		if file != nil {
			_ = file.Close()
		}
		t.Fatal("platform reader followed a symlink instead of opening its reparse point")
	}
	if file, err := openRegularFile(link); err == nil || file != nil {
		if file != nil {
			_ = file.Close()
		}
		t.Fatal("common reader accepted symlink content")
	}
}
