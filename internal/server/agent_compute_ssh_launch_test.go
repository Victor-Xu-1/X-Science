package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	workspace "synon-go/internal/persistence/workspace"
)

func TestAgentSSHProbeObservesCompletionAtProcessExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the production SSH command path requires a Unix shell")
	}
	root := t.TempDir()
	remote := filepath.Join(root, ".synon-biomed", "jobs", "job-completion-identity")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, ".wrapper_pid"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Publish the real terminal file precisely between the probe's first file
	// check and its failed process-liveness check, without relying on a sleep.
	ssh := "#!/usr/bin/env bash\nset -eu\nexec bash -c " + shellSingleQuote(
		"kill() { printf 'done:0:2' > .phase; return 1; }; eval \"$1\"") + " fixture \"${!#}\"\n"
	if err := os.WriteFile(filepath.Join(root, "ssh"), []byte(ssh), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	status, err := pollAgentSSHJob(t.Context(), workspace.ComputeProvider{Name: "ssh:fixture", Family: "ssh"}, remote)
	if err != nil || status.Phase != "done" || status.Exit != 0 || status.Wall != 2 {
		t.Fatalf("completion at process exit was lost: status=%#v err=%v", status, err)
	}
}

func TestAgentSSHLaunchPublishesIdentityBeforeChildStarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the production SSH command path requires a Unix shell")
	}
	realNohup, err := exec.LookPath("nohup")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	remote := filepath.Join(root, ".synon-biomed", "jobs", "job-startup-identity")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	archive := tarGzipFixture(t, map[string]string{
		"_operon_wrapper.sh": "#!/usr/bin/env bash\nprintf 'started\\n' >> starts; printf 'done:0:0' > .phase\n",
	})
	if err := os.WriteFile(filepath.Join(remote, "in.tar.gz"), archive, 0o600); err != nil {
		t.Fatal(err)
	}
	// Hold the child before it can run any wrapper code. The actual launch and
	// probe shells must still agree on a live process identity during this gap.
	gate := filepath.Join(root, "release-child")
	for name, script := range map[string]string{
		"ssh": "#!/usr/bin/env bash\nset -eu\nexec bash -c \"${!#}\"\n",
		"nohup": "#!/usr/bin/env bash\nset -eu\nexec " + shellSingleQuote(realNohup) +
			" bash -c 'while [ ! -f \"$SYNON_TEST_SSH_LAUNCH_GATE\" ]; do " +
			"[ \"$SECONDS\" -lt 10 ] || exit 70; sleep 0.01; done; exec \"$@\"' fixture \"$@\"\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SYNON_TEST_SSH_LAUNCH_GATE", gate)
	provider := workspace.ComputeProvider{Name: "ssh:fixture", Family: "ssh"}
	server := &Server{}
	t.Cleanup(func() {
		if err := os.WriteFile(gate, nil, 0o600); err != nil {
			t.Error(err)
			return
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if phase, err := os.ReadFile(filepath.Join(remote, ".phase")); err == nil && string(phase) == "done:0:0" {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("released launch fixture did not publish its terminal phase")
	})
	if err := server.launchAgentSSHJob(t.Context(), provider, remote, time.Minute, "none", nil, nil); err != nil {
		t.Fatal(err)
	}
	firstPID, err := os.ReadFile(filepath.Join(remote, ".wrapper_pid"))
	if err != nil || strings.TrimSpace(string(firstPID)) == "" {
		t.Fatalf("launch returned without a child identity: pid=%q err=%v", firstPID, err)
	}
	status, err := pollAgentSSHJob(t.Context(), provider, remote)
	if err != nil || status.Phase != "running" {
		t.Fatalf("blocked but live child status=%#v err=%v", status, err)
	}
	if err := server.launchAgentSSHJob(t.Context(), provider, remote, time.Minute, "none", nil, nil); err != nil {
		t.Fatal(err)
	}
	secondPID, err := os.ReadFile(filepath.Join(remote, ".wrapper_pid"))
	if err != nil || string(secondPID) != string(firstPID) {
		t.Fatalf("duplicate launch replaced the live identity: first=%q second=%q err=%v", firstPID, secondPID, err)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		status, err := pollAgentSSHJob(ctx, provider, remote)
		if err != nil {
			t.Fatal(err)
		}
		if status.Phase == "done" {
			starts, err := os.ReadFile(filepath.Join(remote, "starts"))
			if err != nil || string(starts) != "started\n" || status.Exit != 0 {
				t.Fatalf("wrapper did not complete exactly once: starts=%q status=%#v err=%v", starts, status, err)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("released wrapper did not complete")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
