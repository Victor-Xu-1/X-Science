package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"synon-go/internal/compute/checkpoint"
	"synon-go/internal/compute/transfer"
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
	err = transport.Fetch(ctx, "checkpoint-"+key+".jsonl", filepath.Join(stage, "checkpoint.jsonl"), receipt)
	if errors.Is(err, errComputeHarvestPending) {
		return false, nil
	}
	return true, err
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
	receipt, err := checkpoint.Read(ctx, manifest, stringValue(hardware["archive_sha256"]), contract.CommandSHA256(), func(file checkpoint.File) error { return index.Add(ctx, file.Path) })
	if err != nil {
		_ = index.Close()
		return nil, "", nil
	} // Keep ordinary results deliverable; no restartability is claimed.
	if err := index.Add(ctx, contract.Manifest); err != nil {
		_ = index.Close()
		return nil, "", nil
	}
	return index, receipt.ManifestSHA256, nil
}
