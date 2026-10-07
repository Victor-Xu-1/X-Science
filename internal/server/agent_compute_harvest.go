package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"synon-go/internal/compute/transfer"
)

type computeHarvestTransport interface {
	Install(context.Context, string) error
	Control(context.Context, string, string, string) (transfer.ControlReceipt, error)
	Select(context.Context, string, string, string) error
	Fetch(context.Context, string, string, transfer.ControlReceipt) error
}

var errComputeHarvestPending = errors.New("output transfer verification remains in progress")

type computeHarvestSelection struct {
	Selection  transfer.Selection `json:"selection"`
	ListSHA256 string             `json:"list_sha256"`
}

type computeHarvestResult struct {
	Files          []string
	Count          int64
	Left           []map[string]any
	RemoteCount    int64
	Manifest       string
	ManifestSHA256 string
}

// One authority selects before native packaging for every compute transport.
// A control pass is bounded; detached packaging and durable partial transport
// survive the next probe or controller reconstruction without rerunning work.
func (s *Server) harvestSelectedComputeOutputs(ctx context.Context, stage, workspaceDir, jobID string,
	hardware map[string]any, remoteURI func(string) string, transport computeHarvestTransport,
) (result computeHarvestResult, ready bool, err error) {
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return result, false, err
	}
	resolved, err := canonicalHostDirectory(stage)
	if err != nil || resolved != filepath.Clean(stage) {
		return result, false, errors.New("compute harvest stage authority changed")
	}
	workspaceRoot, err := canonicalHostDirectory(workspaceDir)
	if err != nil {
		return result, false, err
	}
	if err := transport.Install(ctx, filepath.Join(s.runtimeAssetsDir, "compute", "harvest.sh.tmpl")); err != nil {
		return result, false, err
	}
	observation, err := transport.Control(ctx, "inventory", "", "")
	if err != nil {
		return result, false, err
	}
	if observation.State != "ready" {
		if observation.State == "failed" {
			return result, false, errors.New("remote output inventory failed; original results retained")
		}
		return result, false, nil
	}
	if err := transport.Fetch(ctx, "inventory.nul", filepath.Join(stage, "inventory.nul"), observation); err != nil {
		if errors.Is(err, errComputeHarvestPending) {
			return result, false, nil
		}
		return result, false, err
	}
	if ready, err := fetchComputeCheckpointManifest(ctx, stage, hardware, transport); err != nil || !ready {
		return result, false, err
	}
	selection, err := prepareComputeHarvestSelection(ctx, stage, workspaceRoot, hardware, observation)
	if err != nil {
		return result, false, err
	}
	if err := transport.Select(ctx, filepath.Join(stage, "selection.nul"), observation.SHA256, selection.ListSHA256); err != nil {
		return result, false, err
	}
	packaged, err := transport.Control(ctx, "archive", observation.SHA256, selection.ListSHA256)
	if err != nil {
		return result, false, err
	}
	if packaged.State != "ready" {
		if packaged.State == "failed" {
			return result, false, errors.New("remote output packaging failed; original results retained")
		}
		return result, false, nil
	}
	archiveName := "archive-" + observation.SHA256 + "-" + selection.ListSHA256 + ".tar.gz"
	archivePath := filepath.Join(stage, "out.tar.gz")
	if err := transport.Fetch(ctx, archiveName, archivePath, packaged); err != nil {
		if errors.Is(err, errComputeHarvestPending) {
			return result, false, nil
		}
		return result, false, err
	}
	if err := transfer.VerifyFile(ctx, archivePath, packaged.SHA256, packaged.Bytes); err != nil {
		return result, false, err
	}
	target := filepath.Join(workspaceRoot, "hpc", jobID)
	extracted, err := transfer.ExtractSelected(ctx, archivePath, filepath.Join(stage, "output-manifest.jsonl"), target, selection.Selection)
	if err != nil {
		return result, false, err
	}
	for _, name := range extracted.Files {
		result.Files = append(result.Files, filepath.ToSlash(filepath.Join("hpc", jobID, name)))
	}
	result.Count, result.RemoteCount = extracted.Count, selection.Selection.RemoteFiles
	result.Manifest = filepath.ToSlash(filepath.Join("hpc", jobID, "output-manifest.jsonl"))
	result.ManifestSHA256 = selection.Selection.ManifestSHA256
	if err := publishComputeOutputManifest(ctx, filepath.Join(stage, "output-manifest.jsonl"), filepath.Join(target, "output-manifest.jsonl"), result.ManifestSHA256); err != nil {
		return result, false, err
	}
	for _, record := range selection.Selection.RemotePreview {
		result.Left = append(result.Left, map[string]any{"uri": remoteURI(record.Path), "path": record.Path, "size": record.Bytes, "reason": record.Reason})
	}
	return result, true, nil
}

func prepareComputeHarvestSelection(ctx context.Context, stage, workspaceRoot string, hardware map[string]any,
	observation transfer.ControlReceipt,
) (result computeHarvestSelection, err error) {
	summaryPath := filepath.Join(stage, "selection.json")
	if info, statErr := os.Lstat(summaryPath); statErr == nil {
		if !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return result, errors.New("compute selection receipt invalid")
		}
		raw, readErr := os.ReadFile(summaryPath)
		if readErr != nil || json.Unmarshal(raw, &result) != nil || result.Selection.SourceSHA256 != observation.SHA256 {
			return result, errors.New("compute selection identity changed")
		}
		for name, digest := range map[string]string{"output-manifest.jsonl": result.Selection.ManifestSHA256, "selection.nul": result.ListSHA256} {
			info, err := os.Lstat(filepath.Join(stage, name))
			if err != nil {
				return result, err
			}
			if err := transfer.VerifyFile(ctx, filepath.Join(stage, name), digest, info.Size()); err != nil {
				return result, err
			}
		}
		return result, nil
	} else if !os.IsNotExist(statErr) {
		return result, statErr
	}
	policy, err := agentComputeOutputPolicy(anySliceValue(hardware["outputs"]))
	if err != nil {
		return result, err
	}
	required, requiredDigest, err := computeCheckpointSelection(ctx, stage, hardware)
	if err != nil {
		return result, err
	}
	if required != nil {
		defer required.Close()
		policy.RequiredPaths, policy.RequiredSHA256 = required, requiredDigest
	}
	limits := mapValue(hardware["transfer_limits"])
	policy.Exclude = stringArrayValue(hardware["exclude"])
	policy.MaxFileBytes, policy.MaxTotalBytes = int64(numberValue(limits["max_file_bytes"])), int64(numberValue(limits["max_total_bytes"]))
	capacity, err := transfer.AvailableStorage(workspaceRoot)
	if err != nil {
		return result, err
	}
	stageCapacity, err := transfer.AvailableStorage(stage)
	if err != nil {
		return result, err
	}
	// Reserve simultaneous compressed staging, extraction and manifest/index
	// work on the actual filesystems; this is not a global dataset ceiling.
	capacity.Bytes = max(0, min(capacity.Bytes, stageCapacity.Bytes)-(64<<20)) / 3
	capacity.Files = max(0, min(capacity.Files, stageCapacity.Files)-16) / 2
	source, err := os.Open(filepath.Join(stage, "inventory.nul"))
	if err != nil {
		return result, err
	}
	defer source.Close()
	manifest, err := os.CreateTemp(stage, ".manifest-")
	if err != nil {
		return result, err
	}
	defer os.Remove(manifest.Name())
	selected, err := os.CreateTemp(stage, ".selected-")
	if err != nil {
		_ = manifest.Close()
		return result, err
	}
	defer os.Remove(selected.Name())
	digest := sha256.New()
	result.Selection, err = transfer.BuildSelection(ctx, source, observation.SHA256, policy, capacity, manifest, io.MultiWriter(selected, digest), stage)
	closeErr := errors.Join(manifest.Sync(), selected.Sync(), manifest.Close(), selected.Close())
	if err != nil || closeErr != nil {
		return result, errors.Join(err, closeErr)
	}
	result.ListSHA256 = hex.EncodeToString(digest.Sum(nil))
	if err := os.Rename(manifest.Name(), filepath.Join(stage, "output-manifest.jsonl")); err != nil {
		return result, err
	}
	if err := os.Rename(selected.Name(), filepath.Join(stage, "selection.nul")); err != nil {
		return result, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(summaryPath+".next", raw, 0o600); err != nil {
		return result, err
	}
	return result, os.Rename(summaryPath+".next", summaryPath)
}

func publishComputeOutputManifest(ctx context.Context, source, target, digest string) error {
	if info, err := os.Lstat(target); err == nil {
		return transfer.VerifyFile(ctx, target, digest, info.Size())
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(target), ".output-manifest-")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	_, copyErr := io.Copy(output, input)
	if err := errors.Join(copyErr, output.Sync(), output.Close()); err != nil {
		return err
	}
	return os.Link(output.Name(), target)
}
