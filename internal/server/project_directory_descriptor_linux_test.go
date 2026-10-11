package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestPreparedProjectDescriptorPreflightIsolatesStaticLimit(t *testing.T) {
	const childFlag = "XSCI_PREP_DESCRIPTOR_CHILD"
	if os.Getenv(childFlag) != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, executable, "-test.run=^TestPreparedProjectDescriptorPreflightIsolatesStaticLimit$", "-test.count=1", "-test.v")
		command.Env = append(os.Environ(), childFlag+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated descriptor fixture: %v\n%s", err, output)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "prepared")
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	maxFD := 2
	for _, entry := range entries {
		if fd, err := strconv.Atoi(entry.Name()); err == nil && fd > maxFD {
			maxFD = fd
		}
	}
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original); err != nil {
		t.Fatal(err)
	}
	limited := original
	limited.Cur = uint64(maxFD + 4)
	if limited.Cur > original.Cur {
		limited.Cur = original.Cur
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limited); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original) }()
	var held []*os.File
	defer func() {
		for _, file := range held {
			_ = file.Close()
		}
	}()
	for len(held) < int(limited.Cur) {
		file, err := os.Open(os.DevNull)
		if errors.Is(err, syscall.EMFILE) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, file)
	}
	if len(held) < 2 {
		t.Fatal("isolated descriptor fixture cannot reserve two slots")
	}
	for _, file := range held[len(held)-2:] {
		_ = file.Close()
	}
	held = held[:len(held)-2]
	resource, err := prepareManagedProjectWorkspace(path)
	if resource.Release != nil {
		resource.Release()
	}
	if !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("descriptor preflight error=%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("static descriptor exhaustion created an orphan: %v", err)
	}
}
