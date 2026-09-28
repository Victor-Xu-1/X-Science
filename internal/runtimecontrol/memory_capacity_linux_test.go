//go:build linux

package runtimecontrol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxDecodeCapacityIncludesAncestorLimits(t *testing.T) {
	proc, group := t.TempDir(), t.TempDir()
	write := func(root, name, value string) {
		t.Helper()
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(proc, "meminfo", "MemAvailable: 4096 kB\n")
	write(proc, "self/cgroup", "0::/parent/child\n")
	write(group, "parent/child/memory.max", "max")
	write(group, "parent/memory.max", "1000")
	write(group, "parent/memory.current", "250")
	if got := linuxAvailableMemory(proc, group); got != 750 {
		t.Fatalf("ancestor budget=%d", got)
	}
	write(group, "parent/memory.current", "1001")
	if got := linuxAvailableMemory(proc, group); got != 0 {
		t.Fatalf("exhausted budget=%d", got)
	}
	write(group, "parent/memory.max", "invalid")
	if got := linuxAvailableMemory(proc, group); got != 0 {
		t.Fatalf("invalid budget=%d", got)
	}
	write(group, "parent/memory.max", "max")
	if got := linuxAvailableMemory(proc, group); got != 4096*1024 {
		t.Fatalf("host budget=%d", got)
	}
	write(proc, "self/cgroup", "0::/parent/../child\n")
	if got := linuxAvailableMemory(proc, group); got != 0 {
		t.Fatalf("invalid path budget=%d", got)
	}
}
