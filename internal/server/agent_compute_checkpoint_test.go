package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"synon-go/internal/compute/checkpoint"
	workspace "synon-go/internal/persistence/workspace"
)

func TestComputeNativeCheckpointSelectionResumeAndLogicalBudget(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "checkpoint-owner", "checkpoint-project", "checkpoint-frame")
	if _, err := store.UpsertSSHProvider(workspace.ComputeProviderInput{Name: "ssh:fixture", UserID: "checkpoint-owner", Family: "ssh"}); err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccess("checkpoint-frame")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	root := repositoryRootForServerTest(t)
	work, remote := t.TempDir(), t.TempDir()
	server := &Server{workspaceStore: store, fileRoot: t.TempDir(), runtimeAssetsDir: filepath.Join(root, "assets/optional")}
	contract := checkpoint.Contract{Manifest: "out/native.jsonl", ResumeCommand: "native-worker --resume out/state.bin", Signal: "TERM"}
	jobID := "job-0123456789abcdef01234567"
	_, inputArchive, err := server.stageAgentBYOCJob(jobID, work, map[string]any{"command": "native-worker", "checkpoint": contract}, access)
	if err != nil {
		t.Fatal(err)
	}
	frame, rootFrame, origin := access.Frame.ID, access.Frame.RootFrameID, "checkpoint-origin"
	hardware := map[string]any{"archive_sha256": inputArchive.SHA256, "workspace_dir": work, "root_frame_incarnation_id": access.RootFrameIncarnationID, "outputs": []any{"*.csv"}, "checkpoint_contract": contract, "logical_timeout_seconds": 1000}
	job, err := store.CreateComputeJob(access.UserID, workspace.ComputeJob{JobID: jobID, ProjectID: access.Frame.ProjectID, Provider: "ssh:fixture", Environment: "remote", TierType: "remote", FrameID: &frame, RootFrameID: &rootFrame, OriginToolUseID: &origin, HardwareDetails: hardware, ProviderFamily: "ssh"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.BindComputeJobExternal(access.UserID, jobID, "owned-peer", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(remote, "out"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("native state 17")
	for name, body := range map[string][]byte{"out/state.bin": payload, "out/result.csv": []byte("x\n1\n"), "stdout.log": nil, "stderr.log": nil} {
		if err := os.WriteFile(filepath.Join(remote, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	large, err := os.Create(filepath.Join(remote, "out/unselected.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(40 << 30); err != nil {
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	var manifest bytes.Buffer
	encoder := json.NewEncoder(&manifest)
	for _, record := range []any{checkpoint.Header{Schema: checkpoint.Schema, Generation: 3, SourceInputSHA256: inputArchive.SHA256, ResumeCommandSHA256: contract.CommandSHA256()}, checkpoint.File{Path: "out/state.bin", SHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), Bytes: int64(len(payload))}, checkpoint.Commit{Commit: "complete", FileCount: 1, Bytes: int64(len(payload))}} {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(remote, contract.Manifest), manifest.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(server.fileRoot, "provider-harvests", jobID)
	window, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var harvest computeHarvestResult
	for window.Err() == nil {
		var ready bool
		harvest, ready, err = server.harvestSelectedComputeOutputs(window, stage, work, jobID, hardware, func(name string) string { return "ssh://fixture/" + name }, nativeHarvestTransport{remote})
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if harvest.Count != 5 || harvest.RemoteCount != 1 {
		t.Fatalf("native checkpoint selection=%#v err=%v", harvest, window.Err())
	}
	result := map[string]any{"job_wall_s": 600, "exit_code": 143}
	server.attachComputeCheckpointReceipt(ctx, workspace.OwnedComputeJob{OwnerUserID: access.UserID, Job: job}, result)
	if result["checkpoint_status"] != "verified_native_files" {
		t.Fatal(result)
	}
	if err := store.SetComputeJobResult(access.UserID, jobID, result); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionComputeJob(access.UserID, jobID, workspace.ComputeJobTimedOut, "timeout", time.Now()); err != nil {
		t.Fatal(err)
	}
	resumeInput := map[string]any{"resume_from_job": jobID, "command": contract.ResumeCommand}
	resume, err := server.computeCheckpointResume(ctx, access, resumeInput, work)
	if err != nil || resume.RemainingSeconds != 400 {
		t.Fatal(resume, err)
	}
	nextStage, _, err := server.stageAgentBYOCJob("job-1123456789abcdef01234567", work, resumeInput, access)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.Open(filepath.Join(nextStage, "in.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	state := ""
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "out/state.bin" {
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			state = string(data)
		}
	}
	if state != string(payload) {
		t.Fatal("verified native state was not restored into the successor input archive")
	}
	if err := os.WriteFile(filepath.Join(work, "hpc", jobID, "out/state.bin"), []byte("changed state17"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := server.computeCheckpointResume(ctx, access, resumeInput, work); err == nil {
		t.Fatal("tampered native checkpoint accepted for a successor")
	}
}
