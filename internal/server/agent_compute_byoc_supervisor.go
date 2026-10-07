package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"synon-go/internal/compute/transfer"
	kernelruntime "synon-go/internal/kernel"
	workspace "synon-go/internal/persistence/workspace"
)

const (
	computeProviderJobPollInterval = 15 * time.Second
	computeProviderProbeNamespace  = "compute-provider-job-probes"
)

func (s *Server) RunComputeProviderJobSupervisor(ctx context.Context) error {
	return s.runComputeProviderJobSupervisor(ctx, computeProviderJobPollInterval)
}

func (s *Server) runComputeProviderJobSupervisor(ctx context.Context, interval time.Duration) error {
	if s == nil || s.workspaceStore == nil {
		if ctx != nil {
			<-ctx.Done()
		}
		return nil
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.reconcileActiveComputeProviderJobs(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.computeProviderJobWake:
			s.reconcileActiveComputeProviderJobs(ctx)
		case <-ticker.C:
			s.reconcileActiveComputeProviderJobs(ctx)
		}
	}
}

func (s *Server) reconcileActiveComputeProviderJobs(ctx context.Context) {
	queue := make(chan workspace.OwnedComputeJob)
	var workers sync.WaitGroup
	// Bound control-plane concurrency, never the number or lifetime of jobs.
	for range 4 {
		workers.Go(func() {
			for item := range queue {
				s.reconcileOwnedComputeJob(ctx, item)
			}
		})
	}
	defer func() { close(queue); workers.Wait() }()
	cursor := ""
	for ctx.Err() == nil {
		page, err := s.workspaceStore.ListSupervisedComputeJobsPage(ctx, cursor, workspace.ComputeJobPageMax)
		if err != nil {
			log.Printf("compute supervision inventory unavailable: %v", err)
			return
		}
		for _, owned := range page.Jobs {
			if owned.Job.ProviderFamily == "byoc" && s.providerOperationRunner == nil {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case queue <- owned:
			}
		}
		if page.NextCursor == "" {
			return
		}
		cursor = page.NextCursor
	}
}

func (s *Server) reconcileOwnedComputeJob(ctx context.Context, item workspace.OwnedComputeJob) {
	if !s.claimComputeJobActor(item.OwnerUserID, item.Job.JobID) {
		return
	}
	defer s.releaseComputeJobActor(item.OwnerUserID, item.Job.JobID)
	// A queued inventory snapshot may predate cancellation or completion.
	// Re-read the owned receipt before any external observation/side effect.
	if ctx.Err() != nil {
		return
	}
	current, found, err := s.workspaceStore.GetComputeJob(item.OwnerUserID, item.Job.JobID)
	if err != nil {
		log.Printf("compute supervision receipt unavailable job=%s", item.Job.JobID)
		return
	}
	if !found || isTerminalAgentComputeJobState(current.State) {
		return
	}
	item.Job = current
	if item.Job.ProviderFamily == "ssh" {
		s.reconcileAgentSSHJob(ctx, item)
		return
	}
	s.reconcileComputeProviderJob(ctx, item)
}

func (s *Server) claimComputeJobActor(owner, jobID string) bool {
	key := owner + "\x00" + jobID
	s.computeProviderJobsMu.Lock()
	defer s.computeProviderJobsMu.Unlock()
	if s.computeProviderJobs == nil {
		s.computeProviderJobs = map[string]bool{}
	}
	if s.computeProviderJobs[key] {
		return false
	}
	s.computeProviderJobs[key] = true
	return true
}

func (s *Server) releaseComputeJobActor(owner, jobID string) {
	s.computeProviderJobsMu.Lock()
	delete(s.computeProviderJobs, owner+"\x00"+jobID)
	s.computeProviderJobsMu.Unlock()
}

func (s *Server) reconcileComputeProviderJob(ctx context.Context, owned workspace.OwnedComputeJob) {
	job := owned.Job
	if ctx.Err() != nil || !s.computeProviderProbeDue(job.JobID) {
		return
	}
	if job.FrameID == nil || job.RootFrameID == nil {
		s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, "authority_missing", nil)
		return
	}
	access, found, err := s.workspaceStore.GetKernelFrameAccessContext(ctx, *job.FrameID)
	if err != nil {
		s.handleComputeProviderProbeFailure(owned, err)
		return
	}
	if !found || access.UserID != owned.OwnerUserID || access.Frame.RootFrameID != *job.RootFrameID {
		s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, "authority_changed", nil)
		return
	}
	providerID := strings.TrimPrefix(job.Provider, "byoc:")
	authority, err := s.agentComputeProviderAuthority(access, providerID, true)
	if err != nil {
		s.handleComputeProviderProbeFailure(owned, err)
		return
	}
	hardware := mapValue(job.HardwareDetails)
	installID := strings.TrimSpace(stringValue(hardware["install_id"]))
	if installID == "" || installID != authority.Definition.InstallID {
		s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, "install_identity_changed", nil)
		return
	}
	if job.State == workspace.ComputeJobPending || job.State == workspace.ComputeJobStaging {
		var recovered bool
		job, recovered = s.recoverAgentBYOCSubmission(ctx, owned, access, authority, hardware)
		if !recovered {
			return
		}
		owned.Job = job
	}
	if job.ExternalID == nil || strings.TrimSpace(*job.ExternalID) == "" {
		s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, "remote_identity_missing", nil)
		return
	}
	probeCtx, cancelProbe := context.WithTimeout(ctx, 30*time.Second)
	probe, err := s.providerOperationRunner.RunProviderOperation(probeCtx, kernelruntime.ProviderOperationInput{
		Runtime: authority.Definition, Operation: "wait",
		Request: map[string]any{
			"sandbox_id": *job.ExternalID, "install_id": installID, "poll_seconds": 2, "probe_only": true,
		},
	})
	cancelProbe()
	if err != nil {
		s.handleComputeProviderProbeFailure(owned, err)
		return
	}
	if err := validateComputeProviderProbeReceipt(probe); err != nil {
		s.handleComputeProviderProbeFailure(owned, err)
		return
	}
	if stdout := strings.TrimSpace(stringValue(probe["stdout_tail"])); stdout != "" {
		_ = s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, job.JobID, "stdout", stdout+"\n")
	}
	if stderr := strings.TrimSpace(stringValue(probe["stderr_tail"])); stderr != "" {
		_ = s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, job.JobID, "stderr", stderr+"\n")
	}
	if !boolValue(probe["ready"], false) {
		if err := s.resetOwnedComputeProviderProbe(owned); err != nil {
			log.Printf("compute probe recovery could not be committed job=%s", job.JobID)
		}
		return
	}
	if job.State == workspace.ComputeJobRunning {
		updated, transitionErr := s.workspaceStore.TransitionComputeJob(owned.OwnerUserID, job.JobID, workspace.ComputeJobHarvesting, "", time.Now().UTC())
		if transitionErr != nil {
			return
		}
		job = updated
	}
	workspaceDir := strings.TrimSpace(stringValue(hardware["workspace_dir"]))
	stage := filepath.Join(s.fileRoot, "provider-harvests", job.JobID)
	remoteURI := func(name string) string {
		return "byoc://" + publicComputeProviderName(job.Provider) + "/" + *job.ExternalID + "/work/" + name
	}
	harvest, ready, err := s.harvestSelectedComputeOutputs(ctx, stage, workspaceDir, job.JobID, hardware, remoteURI,
		byocHarvestTransport{runner: s.providerOperationRunner, runtime: authority.Definition, sandbox: *job.ExternalID, stage: stage})
	if err != nil {
		harvesting := workspace.OwnedComputeJob{OwnerUserID: owned.OwnerUserID, Job: job}
		s.handleComputeProviderProbeFailure(harvesting, err)
		return
	}
	if !ready {
		return
	}
	harvestedFiles := harvest.Files
	if err := s.resetOwnedComputeProviderProbe(owned); err != nil {
		log.Printf("compute harvest recovery could not be committed job=%s", job.JobID)
		return
	}
	next, errorKind := classifyComputeProviderTerminal(probe)
	terminalDetails := map[string]any{
		"exit_code":   int(numberValue(probe["job_exit_code"])),
		"job_wall_s":  int(numberValue(probe["job_wall_s"])),
		"stdout_tail": stringValue(probe["stdout_tail"]), "stderr_tail": stringValue(probe["stderr_tail"]),
		"output_files":      harvestedFiles,
		"output_file_count": harvest.Count,
		"output_manifest":   harvest.Manifest, "output_manifest_sha256": harvest.ManifestSHA256,
		"remote_output_count": harvest.RemoteCount, "left_on_remote": harvest.Left,
		"delivery_state":    "complete",
		"featured_files":    featuredComputeProviderFiles(harvestedFiles, anySliceValue(hardware["outputs"])),
		"deadline_fired":    boolValue(probe["deadline_fired"], false),
		"job_timeout_fired": boolValue(probe["job_timeout_fired"], false),
	}
	terminalDetails["output_files_truncated"] = int64(len(harvestedFiles)) < harvest.Count
	if harvest.RemoteCount > 0 {
		terminalDetails["remote_retention_until_epoch"] = hardware["sandbox_deadline_epoch"]
		terminalDetails["remote_retention_is_persistent"] = false
	}
	s.attachComputeCheckpointReceipt(ctx, owned, terminalDetails)
	if err := s.workspaceStore.SetComputeJobResult(owned.OwnerUserID, job.JobID, terminalDetails); err != nil {
		s.handleComputeProviderProbeFailure(owned, err)
		return
	}
	_, err = s.transitionAgentComputeJobTerminal(owned, next, errorKind, time.Now().UTC(), terminalDetails)
	if err != nil {
		return
	}
	handleID := strings.TrimSpace(stringValue(hardware["handle_id"]))
	if handleID != "" {
		s.releaseAgentComputeHandle(handleID, job.JobID, true)
	} else if harvest.RemoteCount == 0 {
		_ = s.terminateAgentBYOCSandbox(context.Background(), authority.Definition, *job.ExternalID)
	}
	_ = os.RemoveAll(stage)
}

func (s *Server) recoverAgentBYOCSubmission(
	ctx context.Context,
	owned workspace.OwnedComputeJob,
	access workspace.KernelFrameAccess,
	authority agentComputeProviderAuthority,
	hardware map[string]any,
) (workspace.ComputeJob, bool) {
	job := owned.Job
	stage, archive, err := s.validateAgentBYOCRecoveryState(job, authority.Definition, hardware)
	if err != nil {
		s.failAgentBYOCRecovery(owned, authority.Definition, workspace.ComputeJobOrphaned, "recovery_state_invalid", err, "", "")
		return job, false
	}
	deadline := time.Unix(int64(numberValue(hardware["sandbox_deadline_epoch"])), 0).UTC()
	if !deadline.After(time.Now()) {
		sandboxID := ""
		if job.ExternalID != nil {
			sandboxID = strings.TrimSpace(*job.ExternalID)
		}
		s.failAgentBYOCRecovery(owned, authority.Definition, workspace.ComputeJobTimedOut, "timeout", errors.New("BYOC submission deadline elapsed before recovery"), sandboxID, stage)
		return job, false
	}
	submissionID := strings.TrimSpace(stringValue(hardware["submission_id"]))
	jobTimeout := time.Duration(numberValue(hardware["job_timeout_seconds"])) * time.Second
	handleID := strings.TrimSpace(stringValue(hardware["handle_id"]))
	sandboxID := ""
	if job.ExternalID != nil {
		sandboxID = strings.TrimSpace(*job.ExternalID)
	}
	if job.State == workspace.ComputeJobPending {
		sandboxID = strings.TrimSpace(stringValue(hardware["sandbox_hint"]))
		if sandboxID != "" && handleID == "" {
			s.failAgentBYOCRecovery(owned, authority.Definition, workspace.ComputeJobOrphaned, "recovery_state_invalid", errors.New("BYOC sandbox hint has no durable handle authority"), "", stage)
			return job, false
		}
		if sandboxID == "" {
			found, findErr := s.providerOperationRunner.RunProviderOperation(ctx, kernelruntime.ProviderOperationInput{
				Runtime: authority.Definition, Operation: "find_owned_submission",
				Request: map[string]any{
					"install_id": authority.Definition.InstallID, "job_id": job.JobID, "submission_id": submissionID,
				},
			})
			if findErr != nil {
				if s.handleComputeProviderProbeFailure(owned, findErr) {
					s.removeAgentBYOCRecoveryStage(stage, job.JobID)
				}
				return job, false
			}
			rawIDs := anySliceValue(found["sandbox_ids"])
			sandboxIDs := stringArrayValue(found["sandbox_ids"])
			if len(rawIDs) != len(sandboxIDs) || len(sandboxIDs) > 2 {
				s.failAgentBYOCRecovery(owned, authority.Definition, workspace.ComputeJobOrphaned, "ambiguous_remote_identity", errors.New("provider returned an invalid owned submission inventory"), "", stage)
				return job, false
			}
			switch len(sandboxIDs) {
			case 0:
				providerSpec := mapValue(hardware["provider_spec"])
				created, createErr := s.createAgentBYOCSandbox(ctx, authority.Definition, job.JobID, submissionID, providerSpec)
				if createErr != nil {
					if s.handleComputeProviderProbeFailure(owned, createErr) {
						s.removeAgentBYOCRecoveryStage(stage, job.JobID)
					}
					return job, false
				}
				sandboxID = strings.TrimSpace(stringValue(created["sandbox_id"]))
			case 1:
				sandboxID = strings.TrimSpace(sandboxIDs[0])
			default:
				s.failAgentBYOCRecovery(owned, authority.Definition, workspace.ComputeJobOrphaned, "ambiguous_remote_identity", errors.New("multiple provider sandboxes match one durable submission"), "", stage)
				return job, false
			}
		}
		if sandboxID == "" || len(sandboxID) > 512 || strings.ContainsAny(sandboxID, "\x00\r\n") {
			s.failAgentBYOCRecovery(owned, authority.Definition, workspace.ComputeJobOrphaned, "remote_identity_invalid", errors.New("provider returned an invalid sandbox id"), "", stage)
			return job, false
		}
		updated, bindErr := s.workspaceStore.BindComputeJobExternalForStaging(owned.OwnerUserID, job.JobID, sandboxID, "")
		if bindErr != nil {
			transient := &kernelruntime.ProviderOperationError{Kind: "transient", Message: "durable sandbox binding could not be committed"}
			if s.handleComputeProviderProbeFailure(owned, transient) {
				_ = s.terminateAgentBYOCSandbox(context.Background(), authority.Definition, sandboxID)
				s.removeAgentBYOCRecoveryStage(stage, job.JobID)
			}
			return job, false
		}
		job = updated
		owned.Job = updated
		if handleID != "" {
			s.bindAgentComputeHandleSandbox(handleID, job.JobID, sandboxID, deadline)
		}
	}
	if job.State != workspace.ComputeJobStaging || sandboxID == "" {
		s.failAgentBYOCRecovery(owned, authority.Definition, workspace.ComputeJobOrphaned, "recovery_state_invalid", errors.New("BYOC recovery did not reach the staging authority"), sandboxID, stage)
		return job, false
	}
	request := agentBYOCSubmissionRequest(authority.Definition.InstallID, submissionID, sandboxID, archive, jobTimeout, deadline, time.Now())
	_, err = s.providerOperationRunner.RunProviderOperation(ctx, kernelruntime.ProviderOperationInput{
		Runtime: authority.Definition, Operation: "submit", Request: request,
		StageDirectory: stage,
		Prepare: func(operationStage string) error {
			observed, err := inspectAgentBYOCStagedArchive(filepath.Join(operationStage, "in.tar.gz"))
			if err == nil && observed != archive {
				return errors.New("BYOC durable input changed")
			}
			return err
		},
	})
	if err != nil {
		if s.handleComputeProviderProbeFailure(owned, err) {
			s.removeAgentBYOCRecoveryStage(stage, job.JobID)
		}
		return job, false
	}
	updated, err := s.workspaceStore.TransitionComputeJob(owned.OwnerUserID, job.JobID, workspace.ComputeJobRunning, "", time.Now().UTC())
	if err != nil {
		_ = s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, job.JobID, "stderr", "submission reached the provider but its running state could not be committed; recovery will retry idempotently\n")
		return job, false
	}
	owned.Job = updated
	if err := s.resetOwnedComputeProviderProbe(owned); err != nil {
		log.Printf("compute submission recovery observation could not be committed job=%s", job.JobID)
	}
	if hardware["checkpoint_contract"] == nil {
		s.removeAgentBYOCRecoveryStage(stage, job.JobID)
	}
	_ = s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, job.JobID, "combined", fmt.Sprintf("recovered submission %d bytes sha256=%s\n", archive.Bytes, archive.SHA256))
	return updated, true
}

func (s *Server) validateAgentBYOCRecoveryState(
	job workspace.ComputeJob,
	runtimeSpec kernelruntime.ProviderRuntimeSpec,
	hardware map[string]any,
) (string, byocInputArchive, error) {
	if s == nil || strings.TrimSpace(s.fileRoot) == "" || !computeJobIDPattern.MatchString(job.JobID) {
		return "", byocInputArchive{}, errors.New("BYOC durable staging authority is unavailable")
	}
	expectedStage := filepath.Clean(filepath.Join(s.fileRoot, "provider-jobs", job.JobID))
	recordedStage := filepath.Clean(strings.TrimSpace(stringValue(hardware["staging_dir"])))
	if recordedStage == "." || recordedStage != expectedStage {
		return "", byocInputArchive{}, errors.New("BYOC durable staging path changed")
	}
	info, err := os.Lstat(expectedStage)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", byocInputArchive{}, errors.New("BYOC durable staging directory is invalid")
	}
	resolved, err := filepath.EvalSymlinks(expectedStage)
	if err != nil || filepath.Clean(resolved) != expectedStage {
		return "", byocInputArchive{}, errors.New("BYOC durable staging directory changed")
	}
	archive, err := inspectAgentBYOCStagedArchive(filepath.Join(expectedStage, "in.tar.gz"))
	if err != nil {
		return "", byocInputArchive{}, err
	}
	expectedArchive := byocInputArchive{
		SHA256: strings.TrimSpace(stringValue(hardware["archive_sha256"])),
		Bytes:  int64(numberValue(hardware["archive_bytes"])),
	}
	if archive != expectedArchive {
		return "", byocInputArchive{}, errors.New("BYOC durable input archive no longer matches its authority record")
	}
	submissionID := strings.TrimSpace(stringValue(hardware["submission_id"]))
	if !byocSubmissionIDPattern.MatchString(submissionID) {
		return "", byocInputArchive{}, errors.New("BYOC durable submission id is invalid")
	}
	jobTimeout := int64(numberValue(hardware["job_timeout_seconds"]))
	deadlineEpoch := int64(numberValue(hardware["sandbox_deadline_epoch"]))
	latestDeadline := job.StartedAt.UTC().Add(byocMaximumContainer + byocHarvestMargin + 5*time.Minute)
	if jobTimeout < 1 || time.Duration(jobTimeout)*time.Second > byocMaximumContainer || deadlineEpoch < 1 || time.Unix(deadlineEpoch, 0).After(latestDeadline) {
		return "", byocInputArchive{}, errors.New("BYOC durable deadline contract is invalid")
	}
	currentConfig := strings.TrimSpace(runtimeSpec.ExtraEnvironment["SYNON_PROVIDER_BOUND_CONFIG_HASH"])
	if currentConfig == "" || strings.TrimSpace(stringValue(hardware["provider_config_sha256"])) != currentConfig {
		return "", byocInputArchive{}, errors.New("BYOC provider configuration changed while the job was recoverable")
	}
	providerSpec := mapValue(hardware["provider_spec"])
	providerSpecSHA256, err := agentBYOCProviderSpecSHA256(providerSpec)
	if err != nil || providerSpecSHA256 != strings.TrimSpace(stringValue(hardware["provider_spec_sha256"])) {
		return "", byocInputArchive{}, errors.New("BYOC provider specification changed while the job was recoverable")
	}
	return expectedStage, archive, nil
}

func (s *Server) failAgentBYOCRecovery(
	owned workspace.OwnedComputeJob,
	runtimeSpec kernelruntime.ProviderRuntimeSpec,
	next string,
	kind string,
	runErr error,
	sandboxID string,
	stage string,
) {
	if !s.failComputeProviderJob(owned, next, kind, runErr) {
		return
	}
	if sandboxID != "" {
		_ = s.terminateAgentBYOCSandbox(context.Background(), runtimeSpec, sandboxID)
	}
	s.removeAgentBYOCRecoveryStage(stage, owned.Job.JobID)
}

func (s *Server) removeAgentBYOCRecoveryStage(stage, jobID string) {
	if s == nil || stage == "" || !computeJobIDPattern.MatchString(jobID) {
		return
	}
	expected := filepath.Clean(filepath.Join(s.fileRoot, "provider-jobs", jobID))
	if filepath.Clean(stage) == expected {
		_ = os.RemoveAll(expected)
	}
}

func (s *Server) handleComputeProviderProbeFailure(owned workspace.OwnedComputeJob, runErr error) bool {
	kind := providerOperationFailureKind(runErr)
	if kind == "not_found" || kind == "ownership_mismatch" {
		return s.failComputeProviderJob(owned, workspace.ComputeJobOrphaned, kind, runErr)
	}
	failures, persistErr := s.deferComputeProviderProbe(owned, kind)
	if persistErr != nil {
		log.Printf("compute control observation could not be committed job=%s kind=%s", owned.Job.JobID, kind)
		return false
	}
	if err := s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, owned.Job.JobID, "stderr", fmt.Sprintf("provider control unavailable (%s); original job retained, next probe after %s\n", kind, computeProviderProbeDelay(failures))); err != nil {
		log.Printf("compute control recovery log could not be committed job=%s", owned.Job.JobID)
	}
	return false
}

func (s *Server) failComputeProviderJob(owned workspace.OwnedComputeJob, next, kind string, runErr error) bool {
	_, err := s.transitionAgentComputeJobTerminal(owned, next, kind, time.Now().UTC(), nil)
	if err != nil {
		return false
	}
	if runErr != nil {
		_ = s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, owned.Job.JobID, "stderr", boundedProviderProvisionError(runErr)+"\n")
	}
	if handleID := strings.TrimSpace(stringValue(mapValue(owned.Job.HardwareDetails)["handle_id"])); handleID != "" {
		s.releaseAgentComputeHandle(handleID, owned.Job.JobID, false)
	}
	return true
}

func (s *Server) transitionAgentComputeJobTerminal(
	owned workspace.OwnedComputeJob,
	next, kind string,
	at time.Time,
	details map[string]any,
) (workspace.ComputeJob, error) {
	if owned.Job.FrameID == nil || owned.Job.RootFrameID == nil {
		return s.workspaceStore.TransitionComputeJob(owned.OwnerUserID, owned.Job.JobID, next, kind, at)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	projected := owned.Job
	if projected.ErrorKind != nil && (*projected.ErrorKind == "control_unreachable" || *projected.ErrorKind == "control_configuration_required") {
		projected.SystemHint = nil
	}
	projected.State = next
	endedAt := at.Format(time.RFC3339Nano)
	projected.EndedAtISO = &endedAt
	if strings.TrimSpace(kind) == "" {
		projected.ErrorKind = nil
	} else {
		errorKind := strings.TrimSpace(kind)
		projected.ErrorKind = &errorKind
	}
	payload := kernelComputeJobProjection(projected)
	for key, value := range details {
		payload[key] = value
	}
	updated, _, _, err := s.workspaceStore.TransitionComputeJobWithNotification(
		context.Background(), owned.OwnerUserID, owned.Job.JobID, next, kind, at,
		workspace.CreateNotificationInput{
			ID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("synon-compute:"+owned.Job.JobID)).String(),
			SenderFrameID: *owned.Job.FrameID, RecipientFrameID: *owned.Job.FrameID, RootFrameID: *owned.Job.RootFrameID,
			OwnerUserID: owned.OwnerUserID, NotificationType: "compute_done", Payload: payload,
		},
	)
	return updated, err
}

func classifyComputeProviderTerminal(probe map[string]any) (string, string) {
	exitCode := int(numberValue(probe["job_exit_code"]))
	if boolValue(probe["deadline_fired"], false) || boolValue(probe["job_timeout_fired"], false) {
		return workspace.ComputeJobTimedOut, "timeout"
	}
	if strings.TrimSpace(stringValue(probe["phase_read_error"])) != "" {
		return workspace.ComputeJobFailed, "phase_invalid"
	}
	if exitCode == 0 {
		return workspace.ComputeJobDone, ""
	}
	return workspace.ComputeJobFailed, "nonzero_exit"
}

func featuredComputeProviderFiles(files []string, outputs []any) []string {
	featured := []string{}
	for _, file := range files {
		if !strings.Contains(file, "/out/") {
			continue
		}
		if len(outputs) == 0 {
			featured = append(featured, file)
		} else {
			for _, raw := range outputs {
				pattern := strings.TrimSpace(stringValue(raw))
				visibility := "featured"
				if item := mapValue(raw); len(item) > 0 {
					pattern = strings.TrimSpace(firstNonEmpty(stringValue(item["glob"]), stringValue(item["path"])))
					visibility = strings.TrimSpace(firstNonEmpty(stringValue(item["visibility"]), "featured"))
				}
				if visibility == "hidden" || pattern == "" {
					continue
				}
				base := strings.TrimPrefix(file[strings.Index(file, "/out/")+1:], "out/")
				candidate := "out/" + base
				if computeOutputGlobMatches(pattern, candidate) {
					featured = append(featured, file)
					break
				}
			}
		}
		if len(featured) == 20 {
			break
		}
	}
	return featured
}

func computeOutputGlobMatches(pattern, candidate string) bool {
	return transfer.GlobMatches(pattern, candidate)
}
