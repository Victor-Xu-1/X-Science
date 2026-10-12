package server

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"x-science/internal/compute/checkpoint"
	"x-science/internal/compute/namespace"
	workspace "x-science/internal/persistence/workspace"
)

type computeNamespaceObservationKey struct{}

// This observation is constructed only from the canonical owner-scoped store.
// It is request-local evidence, not a second persistent job authority.
type computeNamespaceObservation struct {
	job                      namespace.JobBinding
	proof                    namespace.Proof
	checkpointSHA256         string
	originalCheckpointSHA256 string
	outputManifestSHA256     string
	recordCheckpoint         func(context.Context, string) error
	verifyCheckpoint         func(context.Context, string) (bool, error)
}

func computeNamespacePointer(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func computeNamespaceBinding(receipt workspace.ComputeNamespaceCutoverReceipt) namespace.JobBinding {
	return namespace.JobBinding{JobID: receipt.JobID, OwnerUserID: receipt.OwnerUserID,
		ProjectID: receipt.ProjectID, FrameID: receipt.FrameID, RootFrameID: receipt.RootFrameID,
		Environment: receipt.Environment, Provider: receipt.Provider, ExternalID: receipt.ExternalID,
		InputSHA256: receipt.InputSHA256, SourceSHA256: receipt.SourceSHA256}
}

func (s *Server) computeNamespaceContext(ctx context.Context, owned workspace.OwnedComputeJob) (context.Context, error) {
	actual, found, err := s.workspaceStore.GetComputeJob(owned.OwnerUserID, owned.Job.JobID)
	if err != nil || !found || actual.Environment != owned.Job.Environment || actual.Provider != owned.Job.Provider ||
		actual.ProjectID != owned.Job.ProjectID || computeNamespacePointer(actual.ExternalID) != computeNamespacePointer(owned.Job.ExternalID) ||
		computeNamespacePointer(actual.FrameID) != computeNamespacePointer(owned.Job.FrameID) || computeNamespacePointer(actual.RootFrameID) != computeNamespacePointer(owned.Job.RootFrameID) {
		return ctx, errors.Join(namespace.ErrRecoveryUnconfirmed, err)
	}
	frozen, found, err := s.workspaceStore.GetComputeNamespaceCutoverReceipt(ctx, owned.OwnerUserID, owned.Job.JobID)
	if err != nil {
		return ctx, errors.Join(namespace.ErrRecoveryUnconfirmed, err)
	}
	if !found {
		return ctx, nil // Fresh jobs have no historical-format authority.
	}
	binding := computeNamespaceBinding(frozen)
	binding.InputSHA256 = stringValue(mapValue(actual.HardwareDetails)["archive_sha256"])
	proof, err := namespace.Observe(ctx, binding, func(ctx context.Context, owner, jobID string) (namespace.JobBinding, bool, error) {
		receipt, found, err := s.workspaceStore.GetComputeNamespaceCutoverReceipt(ctx, owner, jobID)
		return computeNamespaceBinding(receipt), found, err
	})
	if err != nil {
		return ctx, err
	}
	observation := &computeNamespaceObservation{job: binding, proof: proof,
		outputManifestSHA256:     frozen.OriginalOutputManifestSHA256,
		originalCheckpointSHA256: frozen.OriginalCheckpointManifestSHA256}
	observation.recordCheckpoint = func(ctx context.Context, digest string) error {
		_, err := s.workspaceStore.RecordComputeNamespaceObservedSource(ctx, binding.OwnerUserID, workspace.ComputeNamespaceObservedSourceReceipt{
			JobID: binding.JobID, OwnerUserID: binding.OwnerUserID, SourceKind: "checkpoint",
			SourceSHA256: binding.SourceSHA256, InputSHA256: binding.InputSHA256,
			Environment: binding.Environment, Provider: binding.Provider, ExternalID: binding.ExternalID,
			ManifestSHA256: digest,
		})
		return err
	}
	observation.verifyCheckpoint = func(ctx context.Context, digest string) (bool, error) {
		receipt, found, err := s.workspaceStore.GetComputeNamespaceObservedSource(ctx, binding.OwnerUserID, binding.JobID, "checkpoint", digest)
		if err != nil || !found {
			return false, err
		}
		return receipt.JobID == binding.JobID && receipt.OwnerUserID == binding.OwnerUserID && receipt.SourceSHA256 == binding.SourceSHA256 &&
			receipt.InputSHA256 == binding.InputSHA256 && receipt.Environment == binding.Environment && receipt.Provider == binding.Provider &&
			receipt.ExternalID == binding.ExternalID && receipt.SourceKind == "checkpoint" && receipt.ManifestSHA256 == digest, nil
	}
	return context.WithValue(ctx, computeNamespaceObservationKey{}, observation), nil
}

func computeNamespaceCheckpointSourceAdmitted(ctx context.Context, observation *computeNamespaceObservation, digest string) error {
	if observation == nil || digest == "" {
		return namespace.ErrRecoveryUnconfirmed
	}
	if observation.originalCheckpointSHA256 == digest {
		return nil
	}
	if observation.verifyCheckpoint != nil {
		found, err := observation.verifyCheckpoint(ctx, digest)
		if err == nil && found {
			return nil
		}
		return errors.Join(namespace.ErrRecoveryUnconfirmed, err)
	}
	return namespace.ErrRecoveryUnconfirmed
}

func computeNamespaceSourceProof(ctx context.Context, digest string) (*namespace.SourceProof, error) {
	observation, ok := ctx.Value(computeNamespaceObservationKey{}).(*computeNamespaceObservation)
	if !ok {
		return nil, nil
	}
	proof, err := observation.proof.BindSource(namespace.SourceBinding{JobID: observation.job.JobID,
		Environment: observation.job.Environment, Provider: observation.job.Provider,
		ExternalID: observation.job.ExternalID, ManifestSHA256: digest})
	if err != nil {
		return nil, err
	}
	return &proof, nil
}

func readComputeCheckpointResume(ctx context.Context, source io.ReadSeeker, resume *computeCheckpointResume, consume func(checkpoint.File) error) (checkpoint.Receipt, error) {
	if resume.NamespaceProof != nil {
		return checkpoint.ReadWithHistoricalProof(ctx, source, resume.Receipt.SourceInputSHA256,
			resume.Contract.CommandSHA256(), resume.Receipt.ManifestSHA256, consume, *resume.NamespaceProof)
	}
	return checkpoint.Read(ctx, source, resume.Receipt.SourceInputSHA256, resume.Contract.CommandSHA256(), consume)
}

// A delivery identity gap is not a physical/scientific failure. Retain the
// same job, remote runtime, staged source and previous result for recovery;
// orphaned is the existing bounded observation state, not a resubmission.
func (s *Server) holdComputeNamespaceRecovery(owned workspace.OwnedComputeJob, cause error) bool {
	if !errors.Is(cause, namespace.ErrRecoveryUnconfirmed) {
		return false
	}
	prior, _, err := s.workspaceStore.GetComputeJobResult(owned.OwnerUserID, owned.Job.JobID)
	if err != nil {
		return false
	}
	details := copyMapAny(prior)
	details["delivery_state"] = "held"
	details["physical_outcome"] = "unconfirmed"
	details["logical_task_complete"] = false
	details["recovery_required"] = true
	details["error_kind"] = "namespace_recovery_unconfirmed"
	details["system_hint"] = "The original job and results are preserved. Their runtime or checkpoint identity needs verification before delivery; no replacement computation was started."
	if err := s.workspaceStore.SetComputeJobResult(owned.OwnerUserID, owned.Job.JobID, details); err != nil {
		return false
	}
	if _, err := s.transitionAgentComputeJobTerminal(owned, workspace.ComputeJobOrphaned, "namespace_recovery_unconfirmed", time.Now().UTC(), details); err != nil {
		return false
	}
	_ = s.workspaceStore.AppendComputeJobLog(owned.OwnerUserID, owned.Job.JobID, "stderr", "Delivery recovery held: "+namespace.ErrRecoveryUnconfirmed.Error()+"\n")
	return true
}

// Only a proven historical resume omits old owned control recipes from the
// original input archive. Other file names and all scientific inputs remain
// byte-for-byte unchanged.
func computeHistoricalControlInputName(name string, resume *computeCheckpointResume) bool {
	if resume == nil || resume.NamespaceProof == nil {
		return false
	}
	root := strings.SplitN(name, "/", 2)[0]
	switch root {
	case "_synon_checkpoint.env", "_synon_harvest.sh", ".synon-harvest":
		return true
	default:
		return false
	}
}
