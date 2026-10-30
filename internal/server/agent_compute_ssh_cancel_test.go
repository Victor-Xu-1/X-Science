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

	workspace "x-science/internal/persistence/workspace"
)

func managedSSHCancelFixture(t *testing.T, mode string) (*Server, workspace.KernelFrameAccess, workspace.ComputeJob, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the native SSH fixture requires a Unix remote peer")
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("the native output recovery protocol requires rsync")
	}
	root := t.TempDir()
	remote := filepath.Join(root, ".x-science", "jobs", "job-cancel-outcome")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"ssh":     "#!/usr/bin/env bash\nset -eu\nwhile [[ $1 == -* ]]; do shift 2; done\nshift\ncommand=\"$*\"\nprintf '[%s] %s\\n' \"$$\" \"$command\" >> \"$X_SCIENCE_CANCEL_FIXTURE_TRACE\"\ntrap 'printf \"[%s] exit %s\\n\" \"$$\" \"$?\" >> \"$X_SCIENCE_CANCEL_FIXTURE_TRACE\"' EXIT\nif [[ $X_SCIENCE_CANCEL_FIXTURE_MODE == transport && $command == *'scancel '* ]]; then exit 255; fi\nbash -c \"$command\"\n",
		"scancel": "#!/usr/bin/env bash\nprintf called > .cancel_called\ncase $X_SCIENCE_CANCEL_FIXTURE_MODE in rejected) echo 'Permission denied' >&2; exit 1;; natural) printf 'done:0:1' > .phase;; esac\n",
		"squeue":  "#!/usr/bin/env bash\ncase $X_SCIENCE_CANCEL_FIXTURE_MODE in transport|inventory) exit 1;; foreign) tag=unrelated-job;; *) tag=${PWD##*/};; esac\nprintf '4242|%s\\n' \"$tag\"\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(remote, ".scheduler_id"), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "stdout.log"), []byte("original output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "stderr.log"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("X_SCIENCE_CANCEL_FIXTURE_MODE", mode)
	trace := filepath.Join(root, "protocol.trace")
	t.Setenv("X_SCIENCE_CANCEL_FIXTURE_TRACE", trace)
	t.Cleanup(func() {
		if t.Failed() {
			if raw, err := os.ReadFile(trace); err == nil {
				t.Logf("isolated SSH protocol trace: %s", raw[max(0, len(raw)-6000):])
			}
		}
	})
	store, _, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "cancel-owner", "cancel-project", "cancel-frame")
	if _, err := store.UpsertSSHProvider(workspace.ComputeProviderInput{Name: "ssh:cancel-fixture", UserID: "cancel-owner", Family: "ssh", Scheduler: "slurm"}); err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccessContext(t.Context(), "cancel-frame")
	if err != nil || !found {
		t.Fatalf("fixture authority: found=%v err=%v", found, err)
	}
	frame, rootFrame := access.Frame.ID, access.Frame.RootFrameID
	job, err := store.CreateComputeJob(access.UserID, workspace.ComputeJob{
		JobID: "job-cancel-outcome", ProjectID: access.Frame.ProjectID, Provider: "ssh:cancel-fixture",
		ProviderFamily: "ssh", Environment: "remote", TierType: "remote", FrameID: &frame, RootFrameID: &rootFrame,
		HardwareDetails: map[string]any{"managed_ssh": true, "remote_workdir": remote, "workspace_dir": root,
			"root_frame_incarnation_id": access.RootFrameIncarnationID, "outputs": []any{"stdout.log"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.TransitionComputeJob(access.UserID, job.JobID, workspace.ComputeJobRunning, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server := New(Options{Workspace: store, FileRoot: root, RuntimeAssetsDir: filepath.Join(repositoryRootForServerTest(t), "assets", "optional")})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	return server, access, job, remote
}

// A rejected or unknown request must return its own outcome, not wait until a
// wall clock fabricates a terminal workload result. The final file is only a
// cleanup fence for the old implementation's uncancellable background loop.
func managedSSHCancelReply(t *testing.T, server *Server, access workspace.KernelFrameAccess, job workspace.ComputeJob, remote string, budget time.Duration) map[string]any {
	t.Helper()
	type reply struct {
		value any
		err   error
	}
	done := make(chan reply, 1)
	go func() {
		value, err := server.cancelAgentComputeJob(t.Context(), access, map[string]any{"job_id": job.JobID, "provider": "cancel-fixture"})
		done <- reply{value, err}
	}()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return mapValue(result.value)
	case <-time.After(budget):
		t.Error("cancellation request outcome was hidden behind workload completion")
		if err := os.WriteFile(filepath.Join(remote, ".phase"), []byte("done:0:1"), 0o600); err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatal(result.err)
			}
			return mapValue(result.value)
		case <-time.After(90 * time.Second):
			t.Fatal("original cancellation control failed to drain after terminal cleanup receipt")
		}
	}
	return nil
}

func TestManagedSSHCancelKeepsRejectedAndUnknownJobsSupervised(t *testing.T) {
	for _, mode := range []string{"rejected", "transport", "accepted", "foreign", "inventory"} {
		t.Run(mode, func(t *testing.T) {
			server, access, job, remote := managedSSHCancelFixture(t, mode)
			result := managedSSHCancelReply(t, server, access, job, remote, 2*time.Second)
			current, found, err := server.workspaceStore.GetComputeJob(access.UserID, job.JobID)
			if err != nil || !found {
				t.Fatalf("job readback: found=%v err=%v", found, err)
			}
			if boolValue(result["cancelled"], false) || isTerminalAgentComputeJobState(current.State) || current.EndedAtISO != nil {
				t.Fatalf("unconfirmed request fabricated remote termination: reply=%#v job=%#v", result, current)
			}
			if status := stringValue(result["cancellation_status"]); status != "rejected" && status != "unconfirmed" {
				t.Fatalf("request outcome missing from tool readback: %#v", result)
			}
			active, err := server.runningAgentComputeJobs(access)
			if err != nil || int(numberValue(active["count"])) != 1 {
				t.Fatalf("original job left active supervision: %#v %v", active, err)
			}
			pending, err := server.workspaceStore.CountUnreadNotifications(t.Context(), access.Frame.ID, access.Frame.RootFrameID, access.UserID)
			if err != nil || pending != 0 {
				t.Fatalf("unconfirmed cancellation published a terminal notification: count=%d err=%v", pending, err)
			}
			if mode == "foreign" || mode == "inventory" {
				if _, err := os.Stat(filepath.Join(remote, ".cancel_called")); !os.IsNotExist(err) {
					t.Fatalf("unconfirmed scheduler identity authorized a signal: %v", err)
				}
			}
			if mode == "accepted" {
				if !boolValue(result["cancel_request_accepted"], false) {
					t.Fatalf("missing original request receipt: %#v", result)
				}
				if err := os.WriteFile(filepath.Join(remote, ".phase"), []byte("done:0:1"), 0o600); err != nil {
					t.Fatal(err)
				}
				finishManagedSSHCancelFixture(t, server, access, job)
				settled, found, err := server.workspaceStore.GetComputeJob(access.UserID, job.JobID)
				if err != nil || !found || settled.State != workspace.ComputeJobDone {
					t.Fatalf("unconfirmed request lost eventual result: %#v %v", settled, err)
				}
			}
		})
	}
}

func TestManagedSSHCancelPreservesNaturalCompletion(t *testing.T) {
	server, access, job, remote := managedSSHCancelFixture(t, "natural")
	result := managedSSHCancelReply(t, server, access, job, remote, 30*time.Second)
	finishManagedSSHCancelFixture(t, server, access, job)
	current, found, err := server.workspaceStore.GetComputeJob(access.UserID, job.JobID)
	if err != nil || !found {
		t.Fatalf("job readback: found=%v err=%v", found, err)
	}
	if boolValue(result["cancelled"], false) || current.State != workspace.ComputeJobDone || current.ErrorKind != nil {
		t.Fatalf("natural terminal receipt overwritten by cancellation intent: reply=%#v job=%#v", result, current)
	}
	stored, found, err := server.workspaceStore.GetComputeJobResult(access.UserID, job.JobID)
	if err != nil || !found || !strings.Contains(stringValue(stored["stdout_tail"]), "original output") {
		t.Fatalf("original terminal outputs lost: %#v %v", stored, err)
	}
}

func finishManagedSSHCancelFixture(t *testing.T, server *Server, access workspace.KernelFrameAccess, job workspace.ComputeJob) {
	t.Helper()
	// Native packaging may finish after the request. Only the existing durable
	// supervisor reconciles it, including after an unconfirmed cancellation.
	supervisorCtx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.runComputeProviderJobSupervisor(supervisorCtx, 20*time.Millisecond)
	}()
	t.Cleanup(func() { stop(); <-done })
	if _, err := server.waitAgentComputeJob(t.Context(), access, map[string]any{"job_id": job.JobID, "provider": "cancel-fixture", "timeout_seconds": 10}); err != nil {
		t.Fatal(err)
	}
}

func TestManagedSSHCancelDoesNotCompeteWithOriginalActor(t *testing.T) {
	server, access, job, remote := managedSSHCancelFixture(t, "accepted")
	if !server.claimComputeJobActor(access.UserID, job.JobID) {
		t.Fatal("fixture actor unavailable")
	}
	defer server.releaseComputeJobActor(access.UserID, job.JobID)
	result := managedSSHCancelReply(t, server, access, job, remote, 2*time.Second)
	if boolValue(result["cancelled"], false) || stringValue(result["cancellation_error_code"]) != "compute_control_busy" {
		t.Fatalf("lost original actor fencing: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(remote, ".cancel_called")); !os.IsNotExist(err) {
		t.Fatalf("busy original actor permitted another signal: %v", err)
	}
}
