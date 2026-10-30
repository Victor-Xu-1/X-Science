package server

import (
	"context"
	"errors"
	"log"
	"path"
	"strings"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

// A cancellation request is not an execution result. These fields describe
// only the observed request/remote receipt; the existing job owns its result.
type agentSSHCancelOutcome struct {
	Status              string
	RequestAccepted     bool
	TerminationObserved bool
	ErrorCode           string
}

func (outcome agentSSHCancelOutcome) reply(job workspace.ComputeJob) map[string]any {
	result := map[string]any{
		// The wrapper receipt proves terminal execution, not its cause. In
		// particular, a successful scancel followed by exit!=0 cannot prove
		// Slurm CANCELLED. Keep the original workload outcome and state.
		"cancelled": false, "job_id": job.JobID, "status": job.State,
		"cancellation_status": outcome.Status, "cancel_request_accepted": outcome.RequestAccepted,
		"termination_observed": outcome.TerminationObserved,
	}
	if outcome.ErrorCode != "" {
		result["cancellation_error_code"] = outcome.ErrorCode
	}
	return result
}

func agentSSHCancelCommand(remoteWorkdir string) string {
	return "cd " + shellSingleQuote(remoteWorkdir) + " || exit 44; " + agentSSHProcessIdentityShell + `
if [ -s .phase ]; then printf completed; exit 0; fi
if [ -s .scheduler_id ]; then
  scheduler_id=$(cat .scheduler_id)
  case "$scheduler_id" in ''|*[!0-9]*) exit 65;; esac
  inventory=$(squeue --noheader --user="$(id -un)" --jobs="$scheduler_id" --format='%i|%k') || exit 75
  matched=
  while IFS='|' read -r candidate comment; do
    [ "$candidate" = "$scheduler_id" ] || continue
    [ "$comment" = ` + shellSingleQuote(path.Base(remoteWorkdir)) + ` ] || exit 65
    matched=1
  done <<< "$inventory"
  [ -n "$matched" ] || exit 75
  scancel "$scheduler_id" || exit $?
  printf requested
elif x_science_process_alive; then
  read -r identity < .wrapper_identity
  pid=${identity%%:*}
  kill -TERM "$pid" || exit $?
  printf requested
else
  printf unresolved
fi`
}

func (s *Server) cancelManagedAgentSSHJob(ctx context.Context, owned workspace.OwnedComputeJob) (agentSSHCancelOutcome, error) {
	unconfirmed := agentSSHCancelOutcome{Status: "unconfirmed"}
	if !s.claimComputeJobActor(owned.OwnerUserID, owned.Job.JobID) {
		unconfirmed.ErrorCode = "compute_control_busy"
		return unconfirmed, nil
	}
	defer func() {
		s.releaseComputeJobActor(owned.OwnerUserID, owned.Job.JobID)
		s.notifyComputeProviderJobSupervisor()
	}()
	current, found, err := s.workspaceStore.GetComputeJob(owned.OwnerUserID, owned.Job.JobID)
	if err != nil || !found {
		return unconfirmed, errors.Join(workspace.ErrComputeJobNotFound, err)
	}
	owned.Job = current
	if isTerminalAgentComputeJobState(current.State) {
		// A stored terminal state (for example orphaned) alone does not prove
		// physical remote termination. Do not upgrade it to new evidence.
		return agentSSHCancelOutcome{Status: "already_terminal"}, nil
	}
	provider, found, err := s.workspaceStore.GetComputeProvider(current.Provider, owned.OwnerUserID)
	if err != nil || !found {
		unconfirmed.ErrorCode = "compute_provider_unavailable"
		return unconfirmed, err
	}
	remoteWorkdir := strings.TrimSpace(stringValue(mapValue(current.HardwareDetails)["remote_workdir"]))
	if !validAgentSSHWorkdir(remoteWorkdir, current.JobID) {
		return unconfirmed, errors.New("SSH cancellation workdir does not belong to the original job")
	}
	request, requestErr := runKernelComputeSSHCommand(ctx, provider, kernelComputeCommandRequest{
		Command: agentSSHCancelCommand(remoteWorkdir), Intent: "Request cancellation of the durable remote job", Timeout: 30 * time.Second,
	})
	exit := int(numberValue(request["exit_code"]))
	marker := strings.TrimSpace(stringValue(request["stdout"]))
	accepted := requestErr == nil && exit == 0 && marker == "requested"
	outcome := agentSSHCancelOutcome{Status: "unconfirmed", RequestAccepted: accepted}
	if requestErr != nil || ctx.Err() != nil || exit < 0 || exit == 255 || exit == 75 || exit == 65 {
		outcome.ErrorCode = "cancel_request_unconfirmed"
	} else if exit != 0 {
		outcome.Status, outcome.ErrorCode = "rejected", "cancel_request_rejected"
	}

	// One bounded observation can prove completion even when cancellation was
	// rejected or its response was lost. Never replace that result with intent.
	status, pollErr := pollAgentSSHJob(ctx, provider, remoteWorkdir)
	if pollErr == nil && status.Phase != "running" {
		outcome.Status, outcome.TerminationObserved = "completed", true
		s.harvestAgentSSHJob(ctx, owned, provider, status)
	} else if pollErr != nil {
		if _, err := s.deferComputeProviderProbe(owned, providerOperationFailureKind(pollErr)); err != nil {
			log.Printf("SSH cancellation observation could not be retained job=%s", current.JobID)
		}
	}
	if err := s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, current.JobID, "stderr",
		"Cancellation observation: "+outcome.Status+". Request acceptance does not establish termination; original execution identity and outputs remain authoritative.\n"); err != nil {
		return outcome, err
	}
	return outcome, nil
}
