//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package datadir

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type namespaceForeignOwnerInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (info namespaceForeignOwnerInfo) Sys() any { return info.stat }

func TestNamespaceForeignOwnerIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original-pointer.json")
	if err := os.WriteFile(path, []byte("original state"), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := *actual.Sys().(*syscall.Stat_t)
	stat.Uid++
	// Changing the real test file's owner would require privilege and could
	// leave a foreign artifact. Exercise the same OS metadata branch directly.
	foreign := namespaceForeignOwnerInfo{FileInfo: actual, stat: &stat}
	if err := namespaceOwned(path, foreign, true); err == nil {
		t.Fatal("foreign owner was accepted")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "original state" {
		t.Fatal("ownership rejection changed source data")
	}
}
