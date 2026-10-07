package server

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"time"

	"synon-go/internal/compute/transfer"
	workspace "synon-go/internal/persistence/workspace"
)

type sshHarvestTransport struct {
	provider       workspace.ComputeProvider
	workdir, stage string
}

func (t sshHarvestTransport) command(ctx context.Context, command string) (string, error) {
	reply, err := runKernelComputeSSHCommand(ctx, t.provider, kernelComputeCommandRequest{Command: "cd " + shellSingleQuote(t.workdir) + " && " + command, Intent: "Recover selected compute outputs", Timeout: 30 * time.Second})
	if err != nil {
		return "", err
	}
	if int(numberValue(mapValue(reply)["exit_code"])) != 0 {
		return "", errors.New("SSH output control has no successful receipt")
	}
	return stringValue(mapValue(reply)["stdout"]), nil
}

func (t sshHarvestTransport) Install(ctx context.Context, asset string) error {
	if _, err := t.command(ctx, "test ! -L _synon_harvest.sh && test ! -L .synon-harvest && mkdir -p .synon-harvest && chmod 700 .synon-harvest"); err != nil {
		return err
	}
	return runAgentSSHCopy(ctx, t.provider, asset, path.Join(t.workdir, "_synon_harvest.sh"), true)
}

func (t sshHarvestTransport) Control(ctx context.Context, action, source, selection string) (transfer.ControlReceipt, error) {
	command := "bash --noprofile --norc -p _synon_harvest.sh " + action
	if action == "archive" || action == "checkpoint" {
		command += " " + shellSingleQuote(source) + " " + shellSingleQuote(selection)
	}
	reply, err := t.command(ctx, command)
	if err != nil {
		return transfer.ControlReceipt{}, err
	}
	return transfer.ParseControlReceipt(reply)
}

func (t sshHarvestTransport) Select(ctx context.Context, local, source, selection string) error {
	remote := path.Join(t.workdir, ".synon-harvest", "selection-"+selection+".nul")
	if _, err := t.command(ctx, "test ! -L "+shellSingleQuote(remote)); err != nil {
		return err
	}
	return runAgentSSHCopy(ctx, t.provider, local, remote, true)
}

func (t sshHarvestTransport) Fetch(ctx context.Context, remote, local string, receipt transfer.ControlReceipt) error {
	if filepath.Base(local) != "inventory.nul" && filepath.Base(local) != "out.tar.gz" && filepath.Base(local) != "checkpoint.jsonl" {
		return errors.New("SSH harvest destination invalid")
	}
	if err := runAgentSSHCopy(ctx, t.provider, local, path.Join(t.workdir, ".synon-harvest", remote), false); err != nil {
		return err
	}
	return transfer.VerifyFile(ctx, local, receipt.SHA256, receipt.Bytes)
}
