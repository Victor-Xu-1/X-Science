package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"synon-go/internal/agentruntime"
	kernelruntime "synon-go/internal/kernel"
	workspace "synon-go/internal/persistence/workspace"
)

type agentSSHJobStatus struct {
	Phase string
	Exit  int
	Wall  int
}

type agentSSHRemoteInput struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func (s *Server) submitAgentSSHJob(
	ctx context.Context,
	access workspace.KernelFrameAccess,
	call agentruntime.ToolCall,
	input map[string]any,
	workspaceDir string,
	provider workspace.ComputeProvider,
) (any, error) {
	scheduler, schedulerDirectives, err := agentSSHSchedulerContract(provider, input)
	if err != nil {
		return nil, err
	}
	if err := validateAgentBYOCOutputs(anySliceValue(input["outputs"])); err != nil {
		return nil, err
	}
	exclude, transferLimits, err := agentSSHHarvestContract(input)
	if err != nil {
		return nil, err
	}
	if err := s.enforceAgentComputeCapacity(access, provider); err != nil {
		return nil, err
	}
	digest := sha256String(access.Frame.RootFrameID + "\x00" + call.ID)
	jobID := "job-" + digest[:24]
	if existing, found, getErr := s.workspaceStore.GetComputeJob(access.UserID, jobID); getErr == nil && found {
		if existing.State == workspace.ComputeJobPending || existing.State == workspace.ComputeJobStaging || existing.State == workspace.ComputeJobRunning {
			s.notifyComputeProviderJobSupervisor()
		}
		return kernelComputeJobProjection(existing), nil
	}
	if !s.claimComputeJobActor(access.UserID, jobID) {
		return map[string]any{"job_id": jobID, "status": "submission_in_progress"}, nil
	}
	defer func() { s.releaseComputeJobActor(access.UserID, jobID); s.notifyComputeProviderJobSupervisor() }()
	timeout, err := computeJobTimeout(input, provider.MaxTimeoutSec, 0)
	if err != nil {
		return nil, err
	}
	remoteWorkdir, err := s.agentSSHRemoteWorkdir(ctx, provider, jobID)
	if err != nil {
		return nil, err
	}
	if scheduler == "slurm" {
		probe, probeErr := runKernelComputeSSHCommand(ctx, provider, kernelComputeCommandRequest{
			Command: "command -v sbatch >/dev/null && command -v squeue >/dev/null && command -v scancel >/dev/null",
			Intent:  "Verify the Slurm scheduler commands", Timeout: 30 * time.Second,
		})
		if probeErr != nil || int(numberValue(mapValue(probe)["exit_code"])) != 0 {
			return nil, errors.New("SSH provider is configured for Slurm but sbatch, squeue, or scancel is unavailable")
		}
	}
	input, remoteInputs, err := s.resolveAgentSSHRemoteInputs(ctx, provider, input)
	if err != nil {
		return nil, err
	}
	durableStage, archive, err := s.stageAgentBYOCJob(jobID, workspaceDir, input, access)
	if err != nil {
		return nil, err
	}
	preserveStage := false
	defer func() {
		if !preserveStage {
			_ = os.RemoveAll(durableStage)
		}
	}()
	frameID, rootID, originID := access.Frame.ID, access.Frame.RootFrameID, call.ID
	hardware := map[string]any{
		"managed_ssh": true, "workspace_dir": workspaceDir, "remote_workdir": remoteWorkdir,
		"staging_dir": durableStage, "archive_sha256": archive.SHA256, "archive_bytes": archive.Bytes,
		"outputs": anySliceValue(input["outputs"]), "timeout_seconds": int(timeout / time.Second),
		"scheduler": scheduler, "scheduler_directives": schedulerDirectives,
		"remote_inputs": remoteInputs,
		"exclude":       exclude, "transfer_limits": transferLimits,
	}
	s.computeSubmitMu.Lock()
	if capacityErr := s.enforceAgentComputeCapacity(access, provider); capacityErr != nil {
		s.computeSubmitMu.Unlock()
		return nil, capacityErr
	}
	job, err := s.workspaceStore.CreateComputeJob(access.UserID, workspace.ComputeJob{
		JobID: jobID, ProjectID: access.Frame.ProjectID, Provider: provider.Name,
		Environment: firstNonEmpty(strings.TrimSpace(stringValue(input["environment"])), "remote"),
		TierType:    "remote", FrameID: &frameID, RootFrameID: &rootID, OriginToolUseID: &originID,
		Intent: strings.TrimSpace(stringValue(input["intent"])), HardwareDetails: hardware,
		ProviderFamily: "ssh", ProviderLabel: publicComputeProviderName(provider.Name), SupportsTail: true,
	})
	s.computeSubmitMu.Unlock()
	if err != nil {
		return nil, err
	}
	preserveStage = true
	if err := s.prepareAgentSSHJob(ctx, provider, remoteWorkdir, durableStage, archive); err != nil {
		if _, transitionErr := s.workspaceStore.TransitionComputeJob(access.UserID, jobID, workspace.ComputeJobFailed, "staging_failed", time.Now().UTC()); transitionErr == nil {
			preserveStage = false
		}
		return nil, err
	}
	job, err = s.workspaceStore.BindComputeJobExternalForStaging(access.UserID, jobID, remoteWorkdir, "")
	if err != nil {
		return nil, err
	}
	if err := s.launchAgentSSHJob(ctx, provider, remoteWorkdir, timeout, scheduler, schedulerDirectives, remoteInputs); err != nil {
		// A transport failure after launch may hide an accepted remote job.
		// Keep the same staging identity for reconciliation, never manufacture
		// launch_failed or a new submission from an unknown response.
		return s.retainUnknownComputeSubmission(workspace.OwnedComputeJob{OwnerUserID: access.UserID, Job: job}, err)
	}
	job, err = s.workspaceStore.TransitionComputeJob(access.UserID, jobID, workspace.ComputeJobRunning, "", time.Now().UTC())
	if err != nil {
		return nil, err
	}
	preserveStage = false
	_ = s.workspaceStore.AppendComputeJobLog(access.UserID, jobID, "combined", fmt.Sprintf("submitted %d bytes sha256=%s\n", archive.Bytes, archive.SHA256))
	s.notifyComputeProviderJobSupervisor()
	return map[string]any{
		"job_id": jobID, "provider": publicComputeProviderName(provider.Name), "status": "running",
		"remote_workdir": remoteWorkdir,
	}, nil
}

func agentSSHHarvestContract(input map[string]any) ([]string, map[string]any, error) {
	exclude := stringArrayValue(input["exclude"])
	if len(exclude) != len(anySliceValue(input["exclude"])) || len(exclude) > 256 {
		return nil, nil, errors.New("SSH harvest exclude must contain at most 256 glob strings")
	}
	for _, pattern := range exclude {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || len(pattern) > 4096 || strings.ContainsAny(pattern, "\x00\r\n") || path.IsAbs(pattern) || pattern == ".." || strings.HasPrefix(pattern, "../") {
			return nil, nil, errors.New("SSH harvest exclude contains an invalid glob")
		}
	}
	raw := mapValue(input["transfer_limits"])
	for key := range raw {
		if key != "max_file_mb" && key != "max_total_mb" {
			return nil, nil, fmt.Errorf("SSH transfer_limits field %q is not allowed", key)
		}
	}
	maxFileMB, maxTotalMB := int64(500), int64(2000)
	if value, found := raw["max_file_mb"]; found {
		number := numberValue(value)
		if number <= 0 {
			return nil, nil, errors.New("max_file_mb must be a positive integer")
		}
		maxFileMB = number
	}
	if value, found := raw["max_total_mb"]; found {
		number := numberValue(value)
		if number <= 0 {
			return nil, nil, errors.New("max_total_mb must be a positive integer")
		}
		maxTotalMB = number
	}
	if maxFileMB < 1 || maxTotalMB < 1 || maxFileMB > maxTotalMB || maxTotalMB > 20480 {
		return nil, nil, errors.New("SSH transfer limits require 1 <= max_file_mb <= max_total_mb <= 20480")
	}
	return exclude, map[string]any{
		"max_file_bytes": maxFileMB << 20, "max_total_bytes": maxTotalMB << 20,
	}, nil
}

func (s *Server) resolveAgentSSHRemoteInputs(
	ctx context.Context,
	provider workspace.ComputeProvider,
	input map[string]any,
) (map[string]any, []agentSSHRemoteInput, error) {
	canonical := copyMapAny(input)
	localInputs := []any{}
	remoteInputs := []agentSSHRemoteInput{}
	destinations := map[string]bool{}
	for _, raw := range anySliceValue(input["inputs"]) {
		item := mapValue(raw)
		source := strings.TrimSpace(stringValue(item["src"]))
		if text, ok := raw.(string); ok {
			source = strings.TrimSpace(text)
		}
		destination := strings.TrimSpace(firstNonEmpty(stringValue(item["dst"]), path.Base(source)))
		if !strings.HasPrefix(source, "ssh://") {
			if destinations[destination] {
				return nil, nil, errors.New("compute inputs contain a duplicate destination")
			}
			destinations[destination] = true
			localInputs = append(localInputs, raw)
			continue
		}
		parsed, err := url.Parse(source)
		if err != nil || parsed.Scheme != "ssh" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
			publicComputeProviderName(parsed.Host) != publicComputeProviderName(provider.Name) {
			return nil, nil, errors.New("ssh:// compute input must name the selected provider")
		}
		remotePath := path.Clean(parsed.Path)
		if !agentComputeRemotePathPattern.MatchString(remotePath) || remotePath == "/" || strings.Contains(remotePath, "..") || strings.Contains(destination, "/") || destination == "." || destination == ".." {
			return nil, nil, errors.New("ssh:// compute input path or dst is invalid")
		}
		if destinations[destination] {
			return nil, nil, errors.New("compute inputs contain a duplicate destination")
		}
		resolved, err := resolveAgentSSHRemoteInput(ctx, provider, remotePath)
		if err != nil {
			return nil, nil, err
		}
		destinations[destination] = true
		remoteInputs = append(remoteInputs, agentSSHRemoteInput{Source: resolved, Destination: destination})
	}
	canonical["inputs"] = localInputs
	return canonical, remoteInputs, nil
}

func resolveAgentSSHRemoteInput(ctx context.Context, provider workspace.ComputeProvider, source string) (string, error) {
	result, err := runKernelComputeSSHCommand(ctx, provider, kernelComputeCommandRequest{
		Command: "test -f " + shellSingleQuote(source) + " && readlink -f -- " + shellSingleQuote(source),
		Intent:  "Resolve a declared remote compute input", Timeout: 30 * time.Second,
	})
	if err != nil || int(numberValue(mapValue(result)["exit_code"])) != 0 {
		return "", errors.New("ssh:// compute input is not a readable regular file")
	}
	resolved := strings.TrimSpace(stringValue(mapValue(result)["stdout"]))
	allowedRoots := append([]string(nil), provider.DataRoots...)
	if provider.ScratchRoot != "" {
		allowedRoots = append(allowedRoots, provider.ScratchRoot)
	}
	for _, root := range allowedRoots {
		root = path.Clean(strings.TrimSpace(root))
		if agentComputeRemotePathPattern.MatchString(root) && (resolved == root || strings.HasPrefix(resolved, strings.TrimSuffix(root, "/")+"/")) {
			return resolved, nil
		}
	}
	return "", errors.New("ssh:// compute input resolves outside the provider data and scratch roots")
}

func agentSSHSchedulerContract(provider workspace.ComputeProvider, input map[string]any) (string, []string, error) {
	scheduler := strings.ToLower(strings.TrimSpace(firstNonEmpty(stringValue(input["scheduler"]), provider.Scheduler, "none")))
	if scheduler != "none" && scheduler != "slurm" {
		return "", nil, errors.New("SSH scheduler must be none or slurm")
	}
	directives := []string{}
	seenCommand := false
	for _, line := range strings.Split(stringValue(input["command"]), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#SBATCH") {
			if scheduler != "slurm" || seenCommand || (trimmed != "#SBATCH" && !strings.HasPrefix(trimmed, "#SBATCH ")) || len(trimmed) > 512 {
				return "", nil, errors.New("Slurm directives must be bounded #SBATCH lines at the top of command")
			}
			lower := strings.ToLower(trimmed)
			for _, forbidden := range []string{"--array", "--chdir", " -d ", "--wrap", "--output", "--error", "--job-name"} {
				if strings.Contains(" "+lower+" ", forbidden) {
					return "", nil, fmt.Errorf("Slurm directive %q is reserved by the durable runner", forbidden)
				}
			}
			directives = append(directives, trimmed)
			if len(directives) > 64 {
				return "", nil, errors.New("SSH job exceeds 64 scheduler directives")
			}
			continue
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#!") {
			seenCommand = true
		}
	}
	return scheduler, directives, nil
}

func (s *Server) agentSSHRemoteWorkdir(ctx context.Context, provider workspace.ComputeProvider, jobID string) (string, error) {
	root := strings.TrimSpace(provider.ScratchRoot)
	if root == "" {
		result, err := runKernelComputeSSHCommand(ctx, provider, kernelComputeCommandRequest{
			Command: `printf '%s' "$HOME"`, Intent: "Resolve the remote job root", Timeout: 30 * time.Second,
		})
		if err != nil || int(numberValue(mapValue(result)["exit_code"])) != 0 {
			return "", errors.New("SSH provider home directory could not be resolved")
		}
		root = strings.TrimSpace(stringValue(mapValue(result)["stdout"]))
	}
	if !agentComputeRemotePathPattern.MatchString(root) || root == "/" || strings.Contains(root, "..") {
		return "", errors.New("SSH provider scratch root is not a safe absolute path")
	}
	workdir := path.Join(root, ".synon-biomed", "jobs", jobID)
	if !validAgentSSHWorkdir(workdir, jobID) {
		return "", errors.New("SSH job workdir is invalid")
	}
	return workdir, nil
}

func (s *Server) prepareAgentSSHJob(
	ctx context.Context,
	provider workspace.ComputeProvider,
	remoteWorkdir string,
	stage string,
	archive byocInputArchive,
) error {
	quoted := shellSingleQuote(remoteWorkdir)
	if _, err := runKernelComputeSSHCommand(ctx, provider, kernelComputeCommandRequest{
		Command: "umask 077; mkdir -p -- " + quoted + "; test ! -L " + quoted + "; test \"$(readlink -f -- " + quoted + ")\" = " + quoted,
		Intent:  "Prepare the remote job directory", Timeout: 30 * time.Second,
	}); err != nil {
		return err
	}
	if observed, err := inspectAgentBYOCStagedArchive(filepath.Join(stage, "in.tar.gz")); err != nil || observed != archive {
		return errors.New("SSH staged input archive changed before transfer")
	}
	return runAgentSSHCopy(ctx, provider, filepath.Join(stage, "in.tar.gz"), path.Join(remoteWorkdir, "in.tar.gz"), true)
}

func (s *Server) launchAgentSSHJob(
	ctx context.Context,
	provider workspace.ComputeProvider,
	remoteWorkdir string,
	timeout time.Duration,
	scheduler string,
	directives []string,
	remoteInputs []agentSSHRemoteInput,
) error {
	quoted := shellSingleQuote(remoteWorkdir)
	environment := "OPERON_JOB_TIMEOUT_S=" + strconv.Itoa(int(timeout/time.Second)) +
		" OPERON_SANDBOX_DEADLINE_EPOCH=0 OPERON_SANDBOX_REMAINING_S=0 " +
		"OPERON_HARVEST_MARGIN_S=" + strconv.Itoa(int(byocHarvestMargin/time.Second)) +
		" OPERON_TERM_GRACE_S=" + strconv.Itoa(int(byocTerminationGrace/time.Second))
	command := "set -eu; umask 077; cd " + quoted + "; " + agentSSHProcessIdentityShell +
		"command -v flock >/dev/null || exit 66; exec 9>.launch.lock; flock -w 10 9 || exit 75; " +
		"if [ -s .phase ]; then exit 0; fi; " +
		"if synon_process_alive; then exit 0; fi; "
	if scheduler == "slurm" {
		command += agentSSHSlurmRecoveryShell(path.Base(remoteWorkdir))
	}
	command += "if [ -e .submit_intent ]; then exit 75; fi; env -u TAR_OPTIONS -u GZIP tar -xzf in.tar.gz; "
	for _, remoteInput := range remoteInputs {
		source := shellSingleQuote(remoteInput.Source)
		destination := shellSingleQuote(remoteInput.Destination)
		command += "test -f " + source + "; if [ -L " + destination + " ] && [ \"$(readlink -- " + destination + ")\" = " + source + " ]; then :; " +
			"elif [ -e " + destination + " ] || [ -L " + destination + " ]; then exit 66; else ln -s -- " + source + " " + destination + "; fi; "
	}
	if scheduler == "slurm" {
		script := "#!/usr/bin/env bash\n"
		if len(directives) > 0 {
			script += strings.Join(directives, "\n") + "\n"
		}
		script += "set -eu\ncd " + quoted + "\nexport " + environment + "\n" + agentSSHProcessIdentityShell + "\nsynon_publish_identity $$\nexec bash _operon_wrapper.sh\n"
		command += "printf %s " + shellSingleQuote(script) + " > .synon-slurm.sh; chmod 700 .synon-slurm.sh; " +
			"printf '%s\\n' " + shellSingleQuote(path.Base(remoteWorkdir)) + " > .submit_intent; " +
			"scheduler_id=$(sbatch --parsable --job-name=" + shellSingleQuote(path.Base(remoteWorkdir)) + " --comment=" + shellSingleQuote(path.Base(remoteWorkdir)) + " .synon-slurm.sh 9>&-); scheduler_id=${scheduler_id%%;*}; " +
			"case \"$scheduler_id\" in ''|*[!0-9]*) exit 65;; esac; printf %s \"$scheduler_id\" > .scheduler_id"
	} else {
		// Publish the forked process identity before returning to the monitor.
		// The child may not have been scheduled yet; letting it write the PID
		// creates a window where a live launch is mistaken for a lost job.
		child := "set -eu; " + agentSSHProcessIdentityShell + "synon_publish_identity $$; exec bash _operon_wrapper.sh"
		command += "printf '%s\\n' " + shellSingleQuote(path.Base(remoteWorkdir)) + " > .submit_intent; nohup env " + environment +
			" bash -c " + shellSingleQuote(child) + " 9>&- </dev/null >/dev/null 2>&1 & " +
			"pid=$!; synon_publish_identity \"$pid\" || test -s .phase"
	}
	result, err := runKernelComputeSSHCommand(ctx, provider, kernelComputeCommandRequest{
		Command: command, Intent: "Launch the durable remote job", Timeout: 30 * time.Second,
	})
	if err != nil || int(numberValue(mapValue(result)["exit_code"])) != 0 {
		if err == nil && (int(numberValue(mapValue(result)["exit_code"])) == 65 || int(numberValue(mapValue(result)["exit_code"])) == 66) {
			return &kernelruntime.ProviderOperationError{Kind: "invalid_request", Message: "SSH launch identity or required control commands could not be verified"}
		}
		return &kernelruntime.ProviderOperationError{Kind: "transient", Message: "SSH submission response is unavailable; reconcile the original remote identity before any repeated side effect"}
	}
	return nil
}

func (s *Server) reconcileAgentSSHJob(ctx context.Context, owned workspace.OwnedComputeJob) bool {
	job := owned.Job
	if ctx.Err() != nil || !s.computeProviderProbeDue(job.JobID) {
		return false
	}
	if !boolValue(mapValue(job.HardwareDetails)["managed_ssh"], false) {
		return false
	}
	provider, found, err := s.workspaceStore.GetComputeProvider(job.Provider, owned.OwnerUserID)
	if err != nil {
		return s.handleComputeProviderProbeFailure(owned, err)
	}
	if !found || provider.Family != "ssh" {
		s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, "authority_changed", err)
		return true
	}
	hardware := mapValue(job.HardwareDetails)
	remoteWorkdir := strings.TrimSpace(stringValue(hardware["remote_workdir"]))
	timeout := time.Duration(numberValue(hardware["timeout_seconds"])) * time.Second
	if !validAgentSSHWorkdir(remoteWorkdir, job.JobID) || timeout < 0 {
		s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, "recovery_state_invalid", nil)
		return true
	}
	if job.State == workspace.ComputeJobPending || job.State == workspace.ComputeJobStaging {
		stage, archive, validErr := s.validateAgentSSHRecoveryState(job, hardware)
		if validErr != nil {
			s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, "recovery_state_invalid", validErr)
			return true
		}
		if job.State == workspace.ComputeJobPending {
			if err := s.prepareAgentSSHJob(ctx, provider, remoteWorkdir, stage, archive); err != nil {
				return s.handleComputeProviderProbeFailure(owned, err)
			}
			job, err = s.workspaceStore.BindComputeJobExternalForStaging(owned.OwnerUserID, job.JobID, remoteWorkdir, "")
			if err != nil {
				return false
			}
			owned.Job = job
		}
		scheduler := strings.TrimSpace(firstNonEmpty(stringValue(hardware["scheduler"]), "none"))
		directives := stringArrayValue(hardware["scheduler_directives"])
		remoteInputs, inputErr := storedAgentSSHRemoteInputs(hardware["remote_inputs"])
		if inputErr != nil {
			s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, "recovery_state_invalid", inputErr)
			return true
		}
		if err := s.launchAgentSSHJob(ctx, provider, remoteWorkdir, timeout, scheduler, directives, remoteInputs); err != nil {
			return s.handleComputeProviderProbeFailure(owned, err)
		}
		job, err = s.workspaceStore.TransitionComputeJob(owned.OwnerUserID, job.JobID, workspace.ComputeJobRunning, "", time.Now().UTC())
		if err != nil {
			return false
		}
		s.removeAgentBYOCRecoveryStage(stage, job.JobID)
		owned.Job = job
	}
	status, err := pollAgentSSHJob(ctx, provider, remoteWorkdir)
	if err != nil {
		return s.handleComputeProviderProbeFailure(owned, err)
	}
	if status.Phase == "running" {
		if err := s.resetOwnedComputeProviderProbe(owned); err != nil {
			log.Printf("SSH probe recovery observation could not be committed job=%s", job.JobID)
		}
		return false
	}
	return s.harvestAgentSSHJob(ctx, owned, provider, status)
}

func storedAgentSSHRemoteInputs(value any) ([]agentSSHRemoteInput, error) {
	inputs := []agentSSHRemoteInput{}
	for _, raw := range anySliceValue(value) {
		item := mapValue(raw)
		input := agentSSHRemoteInput{
			Source: strings.TrimSpace(stringValue(item["source"])), Destination: strings.TrimSpace(stringValue(item["destination"])),
		}
		if !agentComputeRemotePathPattern.MatchString(input.Source) || input.Source == "/" || strings.Contains(input.Source, "..") ||
			input.Destination == "" || input.Destination == "." || input.Destination == ".." || strings.Contains(input.Destination, "/") {
			return nil, errors.New("stored SSH remote input is invalid")
		}
		inputs = append(inputs, input)
		if len(inputs) > byocMaximumInputFiles {
			return nil, errors.New("stored SSH remote inputs exceed the bounded contract")
		}
	}
	return inputs, nil
}

func (s *Server) validateAgentSSHRecoveryState(job workspace.ComputeJob, hardware map[string]any) (string, byocInputArchive, error) {
	expected := filepath.Clean(filepath.Join(s.fileRoot, "provider-jobs", job.JobID))
	recorded := filepath.Clean(strings.TrimSpace(stringValue(hardware["staging_dir"])))
	if recorded != expected {
		return "", byocInputArchive{}, errors.New("SSH staging path changed")
	}
	archive, err := inspectAgentBYOCStagedArchive(filepath.Join(expected, "in.tar.gz"))
	if err != nil {
		return "", byocInputArchive{}, err
	}
	want := byocInputArchive{SHA256: strings.TrimSpace(stringValue(hardware["archive_sha256"])), Bytes: int64(numberValue(hardware["archive_bytes"]))}
	if archive != want {
		return "", byocInputArchive{}, errors.New("SSH staged archive changed")
	}
	return expected, archive, nil
}

func pollAgentSSHJob(ctx context.Context, provider workspace.ComputeProvider, remoteWorkdir string) (agentSSHJobStatus, error) {
	// Completion may be published between the first file check and process
	// exit. Recheck the durable terminal receipt before declaring a lost job.
	command := "cd " + shellSingleQuote(remoteWorkdir) + " || exit 44; " +
		agentSSHProcessIdentityShell +
		"if [ -s .phase ]; then cat .phase; exit 0; fi; " +
		agentSSHSlurmObservationShell(path.Base(remoteWorkdir)) +
		"if [ -s .phase ]; then cat .phase; " +
		"elif synon_process_alive; then printf running; " +
		"elif [ -s .phase ]; then cat .phase; " +
		"elif [ -s .wrapper_identity ]; then printf lost; else printf unknown; fi"
	result, err := runKernelComputeSSHCommand(ctx, provider, kernelComputeCommandRequest{
		Command: command, Intent: "Inspect the durable remote job", Timeout: 30 * time.Second,
	})
	if err != nil || int(numberValue(mapValue(result)["exit_code"])) != 0 {
		return agentSSHJobStatus{}, errors.New("SSH job probe failed")
	}
	phase := strings.TrimSpace(stringValue(mapValue(result)["stdout"]))
	if phase == "running" {
		return agentSSHJobStatus{Phase: phase}, nil
	}
	if phase == "lost" || phase == "" {
		return agentSSHJobStatus{}, &kernelruntime.ProviderOperationError{Kind: "not_found", Message: "the original SSH process incarnation is gone without a terminal receipt"}
	}
	if phase == "unknown" {
		return agentSSHJobStatus{}, &kernelruntime.ProviderOperationError{Kind: "transient", Message: "SSH submission identity remains unresolved; no repeated side effect is authorized"}
	}
	parts := strings.Split(phase, ":")
	if len(parts) != 3 || (parts[0] != "done" && parts[0] != "harvest_failed") {
		return agentSSHJobStatus{}, errors.New("SSH job returned an invalid terminal phase")
	}
	exitCode, exitErr := strconv.Atoi(parts[1])
	wall, wallErr := strconv.Atoi(parts[2])
	if exitErr != nil || wallErr != nil || wall < 0 {
		return agentSSHJobStatus{}, errors.New("SSH job returned an invalid terminal result")
	}
	return agentSSHJobStatus{Phase: parts[0], Exit: exitCode, Wall: wall}, nil
}

func validAgentSSHWorkdir(remoteWorkdir, jobID string) bool {
	remoteWorkdir = path.Clean(strings.TrimSpace(remoteWorkdir))
	jobID = strings.TrimSpace(jobID)
	return computeJobIDPattern.MatchString(jobID) && agentComputeRemotePathPattern.MatchString(remoteWorkdir) &&
		strings.HasSuffix(remoteWorkdir, "/.synon-biomed/jobs/"+jobID)
}

func (s *Server) harvestAgentSSHJob(ctx context.Context, owned workspace.OwnedComputeJob, provider workspace.ComputeProvider, status agentSSHJobStatus) bool {
	return s.harvestAgentSSHJobWithOutcome(ctx, owned, provider, status, "", "")
}

func (s *Server) harvestAgentSSHJobWithOutcome(
	ctx context.Context,
	owned workspace.OwnedComputeJob,
	provider workspace.ComputeProvider,
	status agentSSHJobStatus,
	forcedState string,
	forcedKind string,
) bool {
	job := owned.Job
	if status.Phase == "harvest_failed" {
		s.failComputeProviderJob(owned, workspace.ComputeJobFailed, "harvest_failed", errors.New("remote output staging failed"))
		return true
	}
	if job.State == workspace.ComputeJobRunning {
		updated, err := s.workspaceStore.TransitionComputeJob(owned.OwnerUserID, job.JobID, workspace.ComputeJobHarvesting, "", time.Now().UTC())
		if err != nil {
			return false
		}
		job, owned.Job = updated, updated
	}
	hardware := mapValue(job.HardwareDetails)
	remoteWorkdir := strings.TrimSpace(stringValue(hardware["remote_workdir"]))
	// A stable owned staging path keeps verified partial transfer data across
	// probes and controller reconstruction. Remove it only after settlement.
	stage := filepath.Join(s.fileRoot, "provider-harvests", job.JobID)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return s.handleComputeProviderProbeFailure(owned, err)
	}
	resolved, err := canonicalHostDirectory(stage)
	if err != nil || resolved != filepath.Clean(stage) {
		return s.handleComputeProviderProbeFailure(owned, errors.New("SSH harvest staging path is not owned"))
	}
	if err := runAgentSSHCopy(ctx, provider, filepath.Join(stage, "out.tar.gz"), path.Join(remoteWorkdir, "out.tar.gz"), false); err != nil {
		return s.handleComputeProviderProbeFailure(owned, err)
	}
	remoteURI := func(name string) string {
		return "ssh://" + publicComputeProviderName(provider.Name) + path.Join(remoteWorkdir, filepath.ToSlash(name))
	}
	limits := mapValue(hardware["transfer_limits"])
	files, left, err := extractAgentComputeHarvest(
		stage, strings.TrimSpace(stringValue(hardware["workspace_dir"])), job.JobID,
		agentComputeHarvestPolicy{
			Outputs: anySliceValue(hardware["outputs"]), Exclude: stringArrayValue(hardware["exclude"]),
			MaxFileBytes: int64(numberValue(limits["max_file_bytes"])), MaxTotalBytes: int64(numberValue(limits["max_total_bytes"])),
			RemoteURI: remoteURI,
		},
	)
	if err != nil {
		return s.handleComputeProviderProbeFailure(owned, err)
	}
	stdout := readAgentSSHHarvestTail(strings.TrimSpace(stringValue(hardware["workspace_dir"])), job.JobID, "stdout.log")
	stderr := readAgentSSHHarvestTail(strings.TrimSpace(stringValue(hardware["workspace_dir"])), job.JobID, "stderr.log")
	if stdout != "" {
		_ = s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, job.JobID, "stdout", stdout)
	}
	if stderr != "" {
		_ = s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, job.JobID, "stderr", stderr)
	}
	next, kind := workspace.ComputeJobDone, ""
	timeout := int(numberValue(hardware["timeout_seconds"]))
	if timeout > 0 && (status.Exit == 124 || status.Exit == 137) && status.Wall >= timeout-1 {
		next, kind = workspace.ComputeJobTimedOut, "timeout"
	} else if status.Exit != 0 {
		next, kind = workspace.ComputeJobFailed, "nonzero_exit"
	}
	if forcedState != "" {
		next, kind = forcedState, forcedKind
	}
	result := map[string]any{
		"exit_code": status.Exit, "job_wall_s": status.Wall, "stdout_tail": stdout, "stderr_tail": stderr,
		"output_files": files, "output_file_count": len(files),
		"featured_files": featuredComputeProviderFiles(files, anySliceValue(hardware["outputs"])),
		"remote_workdir": remoteWorkdir,
	}
	if len(left) > 0 {
		result["left_on_remote"] = left
		result["system_hint"] = "One or more unselected or over-limit files remain in the remote workdir; retrieve or chain them before close."
	}
	if err := s.resetOwnedComputeProviderProbe(owned); err != nil {
		log.Printf("SSH harvest recovery observation could not be committed job=%s", job.JobID)
		return false
	}
	_ = s.workspaceStore.SetComputeJobResult(owned.OwnerUserID, job.JobID, result)
	_, err = s.transitionAgentComputeJobTerminal(owned, next, kind, time.Now().UTC(), result)
	if err != nil {
		return false
	}
	_ = os.RemoveAll(stage)
	if len(left) == 0 {
		_, _ = runKernelComputeSSHCommand(context.Background(), provider, kernelComputeCommandRequest{
			Command: "rm -rf -- " + shellSingleQuote(remoteWorkdir), Intent: "Remove the harvested remote job directory", Timeout: 30 * time.Second,
		})
	}
	return true
}

func readAgentSSHHarvestTail(workspaceDir, jobID, name string) string {
	path := filepath.Join(workspaceDir, "hpc", jobID, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(raw) > 64<<10 {
		raw = raw[len(raw)-(64<<10):]
	}
	return string(raw)
}

func (s *Server) cancelManagedAgentSSHJob(ctx context.Context, owned workspace.OwnedComputeJob) {
	provider, found, err := s.workspaceStore.GetComputeProvider(owned.Job.Provider, owned.OwnerUserID)
	if err != nil || !found {
		s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, "authority_changed", err)
		return
	}
	remoteWorkdir := strings.TrimSpace(stringValue(mapValue(owned.Job.HardwareDetails)["remote_workdir"]))
	_, _ = runKernelComputeSSHCommand(ctx, provider, kernelComputeCommandRequest{
		Command: "cd " + shellSingleQuote(remoteWorkdir) + " && " + agentSSHProcessIdentityShell + " if [ -s .scheduler_id ]; then scancel \"$(cat .scheduler_id)\" 2>/dev/null || true; elif synon_process_alive; then read -r identity < .wrapper_identity; pid=${identity%%:*}; kill -TERM \"$pid\" 2>/dev/null || true; fi",
		Intent:  "Cancel the durable remote job", Timeout: 30 * time.Second,
	})
	deadline := time.Now().Add(byocTerminationGrace + 15*time.Second)
	for time.Now().Before(deadline) {
		status, pollErr := pollAgentSSHJob(context.Background(), provider, remoteWorkdir)
		if pollErr == nil && status.Phase != "running" {
			if s.harvestAgentSSHJobWithOutcome(context.Background(), owned, provider, status, workspace.ComputeJobFailed, "cancelled") {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	s.failComputeProviderJob(owned, workspace.ComputeJobFailed, "cancelled", errors.New("remote job cancellation did not finish staging before the grace deadline"))
}
