package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"x-science/internal/compute/checkpoint"
	"x-science/internal/compute/namespace"
	"x-science/internal/compute/transfer"
)

func fetchComputeCheckpointManifest(ctx context.Context, stage string, hardware map[string]any, transport computeHarvestTransport) (bool, error) {
	contract, err := checkpoint.Decode(hardware["checkpoint_contract"])
	if err != nil {
		return false, err
	}
	if contract == nil {
		return true, nil
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(contract.Manifest)))
	receipt, err := transport.Control(ctx, "checkpoint", contract.Manifest, key)
	if err != nil {
		return false, err
	}
	if receipt.State == "failed" {
		return true, nil
	} // Optional absent checkpoint is not physical computation failure.
	if receipt.State != "ready" {
		return false, nil
	}
	manifestPath := filepath.Join(stage, "checkpoint.jsonl")
	err = transport.Fetch(ctx, "checkpoint-"+key+".jsonl", manifestPath, receipt)
	if errors.Is(err, errComputeHarvestPending) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := transfer.VerifyFile(ctx, manifestPath, receipt.SHA256, receipt.Bytes); err != nil {
		return false, err
	}
	if observation, ok := ctx.Value(computeNamespaceObservationKey{}).(*computeNamespaceObservation); ok {
		observation.checkpointSHA256 = receipt.SHA256
	}
	return true, nil
}

// Only files in a complete, identity-bound native manifest become required.
// A disk-backed exact index avoids an inventory-sized array of glob rules.
func computeCheckpointSelection(ctx context.Context, stage string, hardware map[string]any) (*transfer.PathIndex, string, error) {
	contract, err := checkpoint.Decode(hardware["checkpoint_contract"])
	if err != nil {
		return nil, "", err
	}
	if contract == nil {
		return nil, "", nil
	}
	manifest, err := os.Open(filepath.Join(stage, "checkpoint.jsonl"))
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer manifest.Close()
	index, err := transfer.NewPathIndex(ctx, stage)
	if err != nil {
		return nil, "", err
	}
	var receipt checkpoint.Receipt
	consume := func(file checkpoint.File) error { return index.Add(ctx, file.Path) }
	if observation, ok := ctx.Value(computeNamespaceObservationKey{}).(*computeNamespaceObservation); ok {
		proof, proofErr := computeNamespaceSourceProof(ctx, observation.checkpointSHA256)
		if proofErr != nil {
			_ = index.Close()
			return nil, "", proofErr
		}
		receipt, err = checkpoint.ReadWithHistoricalProof(ctx, manifest, stringValue(hardware["archive_sha256"]), contract.CommandSHA256(), observation.checkpointSHA256, consume, *proof)
	} else {
		receipt, err = checkpoint.Read(ctx, manifest, stringValue(hardware["archive_sha256"]), contract.CommandSHA256(), consume)
	}
	if err != nil {
		_ = index.Close()
		if errors.Is(err, namespace.ErrRecoveryUnconfirmed) {
			return nil, "", err
		}
		return nil, "", nil
	} // Keep ordinary results deliverable; no restartability is claimed.
	if observation, ok := ctx.Value(computeNamespaceObservationKey{}).(*computeNamespaceObservation); ok {
		if observation.recordCheckpoint == nil {
			_ = index.Close()
			return nil, "", namespace.ErrRecoveryUnconfirmed
		}
		if err := observation.recordCheckpoint(ctx, receipt.ManifestSHA256); err != nil {
			_ = index.Close()
			return nil, "", errors.Join(namespace.ErrRecoveryUnconfirmed, err)
		}
	}
	if err := index.Add(ctx, contract.Manifest); err != nil {
		_ = index.Close()
		return nil, "", nil
	}
	return index, receipt.ManifestSHA256, nil
}
