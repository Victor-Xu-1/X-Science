package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"synon-go/internal/compute/transfer"
	kernelruntime "synon-go/internal/kernel"
)

type byocHarvestTransport struct {
	runner         kernelruntime.ProviderOperationRunner
	runtime        kernelruntime.ProviderRuntimeSpec
	sandbox, stage string
}

func (t byocHarvestTransport) call(ctx context.Context, action string, fields map[string]any) (map[string]any, error) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["sandbox_id"], fields["install_id"], fields["harvest"] = t.sandbox, t.runtime.InstallID, action
	input := kernelruntime.ProviderOperationInput{Runtime: t.runtime, Operation: "wait", Request: fields, StageDirectory: t.stage}
	if action == "fetch" {
		input.TransferIdleTimeout = 5 * time.Minute
	}
	return t.runner.RunProviderOperation(ctx, input)
}

func (t byocHarvestTransport) Install(ctx context.Context, asset string) error {
	if err := copyComputeControlFile(asset, filepath.Join(t.stage, "_synon_harvest.sh")); err != nil {
		return err
	}
	reply, err := t.call(ctx, "install", nil)
	if err == nil && !boolValue(reply["installed"], false) {
		return errors.New("compute helper installation has no receipt")
	}
	return err
}

func (t byocHarvestTransport) Control(ctx context.Context, action, source, selection string) (transfer.ControlReceipt, error) {
	reply, err := t.call(ctx, action, map[string]any{"source_sha256": source, "selection_sha256": selection})
	if err != nil {
		return transfer.ControlReceipt{}, err
	}
	return transfer.ParseControlReceipt(stringValue(reply["receipt"]))
}

func (t byocHarvestTransport) Select(ctx context.Context, local, source, selection string) error {
	reply, err := t.call(ctx, "selection", map[string]any{"source_sha256": source, "selection_sha256": selection})
	if err == nil && !boolValue(reply["selected"], false) {
		return errors.New("compute selection upload has no receipt")
	}
	return err
}

func (t byocHarvestTransport) Fetch(ctx context.Context, remote, local string, receipt transfer.ControlReceipt) error {
	reply, err := t.call(ctx, "fetch", map[string]any{"name": remote, "sha256": receipt.SHA256, "bytes": receipt.Bytes})
	if err != nil {
		return err
	}
	ready, known := reply["ready"].(bool)
	if !known {
		return errors.New("provider output fetch has no explicit readiness receipt")
	}
	if !ready {
		return errComputeHarvestPending
	}
	return transfer.VerifyFile(ctx, local, receipt.SHA256, receipt.Bytes)
}

func copyComputeControlFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("compute control file is not regular")
		}
		if err := os.Remove(target); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Close())
}
