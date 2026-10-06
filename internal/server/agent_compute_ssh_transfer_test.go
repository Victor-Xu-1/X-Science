package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	workspace "synon-go/internal/persistence/workspace"
)

func TestAgentSSHTransferUsesVerifiedNativeProtocolBeyondOldSizeLimit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native Linux SSH transfer protocol")
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("native rsync is required")
	}
	root := t.TempDir()
	ssh := "#!/usr/bin/env bash\nset -eu\nwhile [[ $1 == -* ]]; do shift 2; done\nshift\nexec bash -c \"$*\"\n"
	if err := os.WriteFile(filepath.Join(root, "ssh"), []byte(ssh), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	source := filepath.Join(root, "source")
	f, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate((256 << 20) + 123); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("real protocol fixture"), 16<<20); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	remote, downloaded := filepath.Join(root, "remote"), filepath.Join(root, "download")
	provider := workspace.ComputeProvider{Name: "ssh:fixture", Family: "ssh"}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := runAgentSSHCopy(ctx, provider, source, remote, true); err != nil {
		t.Fatal(err)
	}
	if err := runAgentSSHCopy(ctx, provider, downloaded, remote, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{remote, downloaded} {
		info, err := os.Lstat(path)
		if err != nil || info.Size() != (256<<20)+123 || info.Mode().Perm() != 0o600 {
			t.Fatalf("bad transfer %s: %#v %v", path, info, err)
		}
	}
	// A changed source cannot be skipped merely because an old destination
	// exists. Native rsync's complete-file checksum validates reused data.
	f, err = os.OpenFile(source, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("changed"), 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runAgentSSHCopy(ctx, provider, source, remote, true); err != nil {
		t.Fatal(err)
	}
	if err := runAgentSSHCopy(ctx, provider, downloaded, remote, false); err != nil {
		t.Fatal(err)
	}
	actual, err := os.Open(downloaded)
	if err != nil {
		t.Fatal(err)
	}
	defer actual.Close()
	prefix := make([]byte, 8)
	if _, err := actual.ReadAt(prefix, 0); err != nil || string(prefix[1:]) != "changed" {
		t.Fatalf("stale destination accepted: %q %v", prefix, err)
	}
	if expected, got := sshTransferFixtureDigest(t, source), sshTransferFixtureDigest(t, downloaded); expected != got {
		t.Fatalf("complete transferred bytes changed: want=%s got=%s", expected, got)
	}
}

func sshTransferFixtureDigest(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestAgentSSHTransferRetainsInterruptedPartialDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux protocol")
	}
	root := t.TempDir()
	ssh := "#!/usr/bin/env bash\nexit 0\n"
	if err := os.WriteFile(filepath.Join(root, "ssh"), []byte(ssh), 0o700); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(root, "args")
	rsync := "#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" > " + shellSingleQuote(argsPath) + "\nexit 12\n"
	if err := os.WriteFile(filepath.Join(root, "rsync"), []byte(rsync), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	local := filepath.Join(root, "old-destination")
	if err := os.WriteFile(local, []byte("old data"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := workspace.ComputeProvider{Name: "ssh:fixture", Family: "ssh"}
	err := runAgentSSHCopy(context.Background(), provider, local, "/scratch/output", false)
	if err == nil || providerOperationFailureKind(err) != "transient" {
		t.Fatalf("unknown transfer became success: %v", err)
	}
	data, _ := os.ReadFile(local)
	if string(data) != "old data" {
		t.Fatal("failed transfer deleted existing destination")
	}
	args, err := os.ReadFile(argsPath)
	if err != nil || !strings.Contains(string(args), "--partial-dir=.synon-biomed-partial-") || !strings.Contains(string(args), "--ignore-times\n") || !strings.Contains(string(args), "--timeout=120\n") {
		t.Fatalf("missing verified resumable I/O contract: %s %v", args, err)
	}
}
