package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	if err := os.WriteFile(filepath.Join(remote, ".wrapper_pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, ".wrapper_identity"), []byte(sshTestProcessIdentity(t, os.Getpid())), 0o600); err != nil {
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

func sshTestProcessIdentity(t *testing.T, pid int) string {
	t.Helper()
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(stat)[strings.LastIndex(string(stat), ") ")+2:])
	return strconv.Itoa(pid) + ":" + strings.TrimSpace(string(boot)) + ":" + fields[19] + "\n"
}

func TestAgentSSHProbeRejectsReusedPIDIncarnation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process identity contract")
	}
	root := t.TempDir()
	remote := filepath.Join(root, ".synon-biomed", "jobs", "job-incarnation-fence")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	identity := strings.TrimSpace(sshTestProcessIdentity(t, os.Getpid()))
	identity = identity[:strings.LastIndex(identity, ":")+1] + "0\n"
	if err := os.WriteFile(filepath.Join(remote, ".wrapper_identity"), []byte(identity), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, ".wrapper_pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ssh"), []byte("#!/usr/bin/env bash\nexec bash -c \"${!#}\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := pollAgentSSHJob(t.Context(), workspace.ComputeProvider{Name: "ssh:fixture", Family: "ssh"}, remote)
	if err == nil || providerOperationFailureKind(err) != "not_found" {
		t.Fatalf("reused live PID became the original job: %v", err)
	}
}

func TestAgentSSHSlurmControlFailureDoesNotProveProcessLossAcrossNodes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux remote peer fixture")
	}
	root := t.TempDir()
	remote := filepath.Join(root, ".synon-biomed", "jobs", "job-remote-node")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, ".scheduler_id"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, ".wrapper_identity"), []byte("123:another-node-boot:987\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{"ssh": "#!/usr/bin/env bash\nexec bash -c \"${!#}\"\n", "squeue": "#!/usr/bin/env bash\nexit 1\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	provider := workspace.ComputeProvider{Name: "ssh:fixture", Family: "ssh"}
	_, err := pollAgentSSHJob(t.Context(), provider, remote)
	if err == nil || providerOperationFailureKind(err) == "not_found" {
		t.Fatalf("scheduler connection failure became definitive loss: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "squeue"), []byte("#!/usr/bin/env bash\nprintf '123|job-remote-node\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	status, err := pollAgentSSHJob(t.Context(), provider, remote)
	if err != nil || status.Phase != "running" {
		t.Fatalf("owned compute-node job was lost by login-node PID inspection: %#v %v", status, err)
	}
}

func TestAgentSlurmLaunchReconcilesLostAcceptedResponseWithoutResubmission(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux shell protocol")
	}
	root := t.TempDir()
	remote := filepath.Join(root, ".synon-biomed", "jobs", "job-uncertain-slurm")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "in.tar.gz"), sshLaunchArchiveFixture(t, map[string]string{"_operon_wrapper.sh": "true\n"}), 0o600); err != nil {
		t.Fatal(err)
	}
	// sbatch accepts the request, then loses the response before the local
	// scheduler-id receipt. squeue returns its same stable user-scoped tag.
	scripts := map[string]string{
		"ssh":    "#!/usr/bin/env bash\nexec bash -c \"${!#}\"\n",
		"sbatch": "#!/usr/bin/env bash\nprintf 'submitted\\n' >> submits; printf '987|job-uncertain-slurm\\n' > accepted; exit 75\n",
		"squeue": "#!/usr/bin/env bash\ncat accepted\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := &Server{}
	provider := workspace.ComputeProvider{Name: "ssh:fixture", Family: "ssh"}
	if err := s.launchAgentSSHJob(t.Context(), provider, remote, 0, "slurm", nil, nil); err == nil {
		t.Fatal("lost acceptance response was fabricated as known")
	}
	if err := s.launchAgentSSHJob(t.Context(), provider, remote, 0, "slurm", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.launchAgentSSHJob(t.Context(), provider, remote, 0, "slurm", nil, nil); err != nil {
		t.Fatal(err)
	}
	submits, err := os.ReadFile(filepath.Join(remote, "submits"))
	if err != nil || string(submits) != "submitted\n" {
		t.Fatalf("duplicate side effect: %q %v", submits, err)
	}
	receipt, err := os.ReadFile(filepath.Join(remote, ".scheduler_id"))
	if err != nil || strings.TrimSpace(string(receipt)) != "987" {
		t.Fatalf("accepted identity not recovered: %q %v", receipt, err)
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
	archive := sshLaunchArchiveFixture(t, map[string]string{
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

func sshLaunchArchiveFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	helper, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../assets/optional/compute/harvest.sh.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	files["_synon_harvest.sh"] = string(helper)
	return tarGzipFixture(t, files)
}

func TestAgentSSHLaunchRequiresNativeHarvestReadinessBeforeSideEffect(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux native launch protocol")
	}
	root := t.TempDir()
	remote := filepath.Join(root, ".synon-biomed", "jobs", "job-missing-control")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	archive := tarGzipFixture(t, map[string]string{"_operon_wrapper.sh": "printf started > starts\n"})
	if err := os.WriteFile(filepath.Join(remote, "in.tar.gz"), archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ssh"), []byte("#!/usr/bin/env bash\nexec bash -c \"${!#}\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	server := &Server{}
	if err := server.launchAgentSSHJob(t.Context(), workspace.ComputeProvider{Name: "ssh:fixture", Family: "ssh"}, remote, 0, "none", nil, nil); err == nil {
		t.Fatal("launch accepted an archive missing its required native helper")
	}
	for _, name := range []string{"starts", ".submit_intent", ".wrapper_pid"} {
		if _, err := os.Stat(filepath.Join(remote, name)); !os.IsNotExist(err) {
			t.Fatalf("readiness failure produced a launch side effect %s: %v", name, err)
		}
	}
}
