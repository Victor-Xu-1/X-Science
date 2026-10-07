package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"synon-go/internal/compute/checkpoint"
	workspace "synon-go/internal/persistence/workspace"
)

type computeCheckpointResume struct {
	InputArchive     string
	Contract         checkpoint.Contract
	Receipt          checkpoint.Receipt
	Root             string
	SourceJobID      string
	ElapsedSeconds   int64
	RemainingSeconds int64
	ExplicitBudget   bool
}

func computeNativeCheckpointSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"manifest":       map[string]any{"type": "string", "description": "Committed native JSONL checkpoint manifest under out/."},
		"resume_command": map[string]any{"type": "string", "maxLength": 262144},
		"signal":         map[string]any{"type": "string", "enum": []string{"TERM", "USR1", "USR2"}},
		"pid_file":       map[string]any{"type": "string", "description": "Required for USR1/USR2: native application PID:boot-id:start-ticks receipt under out/. Only the matching process incarnation within this job group can be signalled."},
	}, "required": []string{"manifest", "resume_command", "signal"}}
}

func (s *Server) computeCheckpointResume(ctx context.Context, access workspace.KernelFrameAccess, input map[string]any, workspaceRoot string) (*computeCheckpointResume, error) {
	id := strings.TrimSpace(stringValue(input["resume_from_job"]))
	if id == "" {
		return nil, nil
	}
	job, found, err := s.workspaceStore.GetComputeJob(access.UserID, id)
	if err != nil {
		return nil, err
	}
	if !found || !isTerminalAgentComputeJobState(job.State) || job.RootFrameID == nil || *job.RootFrameID != access.Frame.RootFrameID || job.ProjectID != access.Frame.ProjectID {
		return nil, errors.New("checkpoint source is not a terminal job in this task")
	}
	priorResult, found, err := s.workspaceStore.GetComputeJobResult(access.UserID, id)
	if err != nil || !found {
		return nil, errors.New("checkpoint source has no durable result receipt")
	}
	if stringValue(mapValue(job.HardwareDetails)["root_frame_incarnation_id"]) != access.RootFrameIncarnationID {
		return nil, errors.New("checkpoint source belongs to another task incarnation")
	}
	stored := mapValue(priorResult["checkpoint"])
	contract, err := checkpoint.Decode(stored["contract"])
	if err != nil || contract == nil {
		return nil, errors.New("source job has no verified native checkpoint contract")
	}
	if strings.TrimSpace(stringValue(input["command"])) != strings.TrimSpace(contract.ResumeCommand) {
		return nil, errors.New("checkpoint resume command differs from the original approved contract")
	}
	root := filepath.Join(workspaceRoot, "hpc", job.JobID)
	stage := filepath.Join(s.fileRoot, "provider-checkpoints", job.JobID)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return nil, err
	}
	receipt, err := checkpoint.Verify(ctx, root, stage, *contract, stringValue(mapValue(job.HardwareDetails)["archive_sha256"]))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(stored["receipt"])
	if err != nil {
		return nil, err
	}
	var expected checkpoint.Receipt
	if json.Unmarshal(raw, &expected) != nil || receipt != expected {
		return nil, errors.New("checkpoint receipt changed before resume")
	}
	remaining := int64(numberValue(stored["remaining_timeout_seconds"]))
	explicit := boolValue(stored["explicit_timeout"], false)
	if explicit && remaining <= 0 {
		return nil, errors.New("the original explicit logical execution budget is exhausted")
	}
	if explicit {
		if requested, ok := input["timeout_seconds"]; ok && int64(numberValue(requested)) > remaining {
			return nil, errors.New("checkpoint resume exceeds the remaining explicit logical budget")
		}
	}
	archivePath := filepath.Join(s.fileRoot, "provider-jobs", job.JobID, "in.tar.gz")
	archive, err := inspectAgentBYOCStagedArchive(archivePath)
	if err != nil || archive.SHA256 != stringValue(mapValue(job.HardwareDetails)["archive_sha256"]) {
		return nil, errors.New("the immutable original checkpoint inputs are unavailable or changed")
	}
	return &computeCheckpointResume{InputArchive: archivePath, Contract: *contract, Receipt: receipt, Root: root, SourceJobID: job.JobID, ElapsedSeconds: int64(numberValue(stored["elapsed_seconds"])), RemainingSeconds: remaining, ExplicitBudget: explicit}, nil
}

// Checkpoint validation describes restartability, never scientific success.
// A missing/invalid optional checkpoint does not rewrite the physical exit.
func (s *Server) attachComputeCheckpointReceipt(ctx context.Context, owned workspace.OwnedComputeJob, result map[string]any) {
	hardware := mapValue(owned.Job.HardwareDetails)
	contract, err := checkpoint.Decode(hardware["checkpoint_contract"])
	if err != nil || contract == nil {
		return
	}
	stage := filepath.Join(s.fileRoot, "provider-checkpoints", owned.Job.JobID)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		result["checkpoint_status"] = "storage_unavailable"
		return
	}
	root := filepath.Join(stringValue(hardware["workspace_dir"]), "hpc", owned.Job.JobID)
	receipt, err := checkpoint.Verify(ctx, root, stage, *contract, stringValue(hardware["archive_sha256"]))
	if err != nil || receipt.Generation <= int64(numberValue(hardware["checkpoint_generation"])) {
		result["checkpoint_status"] = "not_restartable"
		return
	}
	elapsed := int64(numberValue(hardware["logical_elapsed_seconds"])) + int64(numberValue(result["job_wall_s"]))
	budget := int64(numberValue(hardware["logical_timeout_seconds"]))
	remaining := int64(0)
	if budget > 0 {
		remaining = max(0, budget-int64(numberValue(result["job_wall_s"])))
	}
	result["checkpoint_status"] = "verified_native_files"
	result["checkpoint"] = map[string]any{"contract": contract, "receipt": receipt, "source_job_id": owned.Job.JobID, "elapsed_seconds": elapsed, "remaining_timeout_seconds": remaining, "explicit_timeout": budget > 0, "resume_from_job": owned.Job.JobID, "logical_task_complete": false}
}

func computeCheckpointTimeoutInput(input map[string]any, resume *computeCheckpointResume) map[string]any {
	if resume == nil || !resume.ExplicitBudget {
		return input
	}
	result := copyMapAny(input)
	requested := int64(numberValue(input["timeout_seconds"]))
	if requested <= 0 || requested > resume.RemainingSeconds {
		result["timeout_seconds"] = resume.RemainingSeconds
	}
	return result
}

func bindComputeCheckpointHardware(hardware map[string]any, contract *checkpoint.Contract, resume *computeCheckpointResume, timeoutSeconds int64) {
	if contract == nil && resume != nil {
		contract = &resume.Contract
	}
	if contract != nil {
		hardware["checkpoint_contract"] = contract
		hardware["logical_timeout_seconds"] = timeoutSeconds
	}
	if resume != nil {
		hardware["checkpoint_generation"] = resume.Receipt.Generation
		hardware["logical_elapsed_seconds"] = resume.ElapsedSeconds
		hardware["checkpoint_source_job_id"] = resume.SourceJobID
	}
}
