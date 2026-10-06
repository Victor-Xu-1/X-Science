package server

import (
	"context"
	"errors"
	"log"
	"time"

	workspace "synon-go/internal/persistence/workspace"
)

// Attempts bound one observation burst, not the lifetime of the logical task.
// Persisted backoff survives controller reconstruction; it never resubmits or
// terminates the remote workload merely because the network is unavailable.
func computeProviderProbeDelay(failures int) time.Duration {
	delay := computeProviderJobPollInterval
	for n := 1; n < failures && delay < 5*time.Minute; n++ {
		delay = min(delay*2, 5*time.Minute)
	}
	return delay
}

func (s *Server) deferComputeProviderProbe(owned workspace.OwnedComputeJob, kind string) (int, error) {
	state := "control_unreachable"
	if kind == "unauthorized" || kind == "invalid_request" {
		state = "control_configuration_required"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Publish the truthful observation before delaying its next check. A
	// secondary backoff-store failure must not leave a stale green live claim.
	if err := s.workspaceStore.SetComputeJobControlUnavailable(ctx, owned.OwnerUserID, owned.Job.JobID, state); err != nil {
		return 0, err
	}
	if s.runtimeStore == nil {
		return 0, errors.New("compute probe persistence is unavailable")
	}
	entry, found, err := s.runtimeStore.Get(computeProviderProbeNamespace, owned.Job.JobID)
	if err != nil {
		return 0, err
	}
	failures := 1
	if found {
		// Backoff saturates; the counter is not a termination threshold.
		failures = min(32, max(0, int(numberValue(mapValue(entry.Value)["count"])))+1)
	}
	now := time.Now().UTC()
	if _, err := s.runtimeStore.SetContext(ctx, computeProviderProbeNamespace, owned.Job.JobID, map[string]any{
		"count": failures, "kind": kind, "updated_at": now,
		"next_probe_at_ms": now.Add(computeProviderProbeDelay(failures)).UnixMilli(),
	}); err != nil {
		return 0, err
	}
	return failures, nil
}

func (s *Server) computeProviderProbeDue(jobID string) bool {
	if s.runtimeStore == nil {
		log.Printf("compute supervision deferred: probe persistence unavailable job=%s", jobID)
		return false
	}
	entry, found, err := s.runtimeStore.Get(computeProviderProbeNamespace, jobID)
	if err != nil {
		log.Printf("compute supervision deferred: probe persistence read failed job=%s", jobID)
		return false
	}
	if !found {
		return true
	}
	next := int64(numberValue(mapValue(entry.Value)["next_probe_at_ms"]))
	return next <= time.Now().UnixMilli()
}

func (s *Server) resetOwnedComputeProviderProbe(owned workspace.OwnedComputeJob) error {
	if s.runtimeStore == nil {
		return errors.New("compute probe persistence is unavailable")
	}
	if err := s.workspaceStore.ClearComputeJobControlUnavailable(context.Background(), owned.OwnerUserID, owned.Job.JobID); err != nil {
		return err
	}
	_, err := s.runtimeStore.Delete(computeProviderProbeNamespace, owned.Job.JobID)
	return err
}

func (s *Server) retainUnknownComputeSubmission(owned workspace.OwnedComputeJob, cause error) (any, error) {
	if _, err := s.deferComputeProviderProbe(owned, providerOperationFailureKind(cause)); err != nil {
		return nil, err
	}
	s.notifyComputeProviderJobSupervisor()
	current, found, err := s.workspaceStore.GetComputeJob(owned.OwnerUserID, owned.Job.JobID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, workspace.ErrComputeJobNotFound
	}
	return kernelComputeJobProjection(current), nil
}
