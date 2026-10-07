package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	kernelruntime "synon-go/internal/kernel"
)

// Control fixture uses the real native protocol. It waits only for its tiny
// local packaging operation; the product supervisor remains nonblocking.
func recordingComputeHarvestOperation(ctx context.Context, input kernelruntime.ProviderOperationInput, action string) (map[string]any, error) {
	root := filepath.Join(input.StageDirectory, "fixture-remote")
	transport := nativeHarvestTransport{root}
	switch action {
	case "install":
		if err := os.MkdirAll(filepath.Join(root, "out"), 0o700); err != nil {
			return nil, err
		}
		for name, body := range map[string]string{"out/result.txt": "harvested-result", "stdout.log": "remote stdout", "stderr.log": ""} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
				return nil, err
			}
		}
		if err := transport.Install(ctx, filepath.Join(input.StageDirectory, "_synon_harvest.sh")); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "installed": true}, nil
	case "selection":
		err := transport.Select(ctx, filepath.Join(input.StageDirectory, "selection.nul"), stringValue(input.Request["source_sha256"]), stringValue(input.Request["selection_sha256"]))
		return map[string]any{"ok": err == nil, "selected": err == nil}, err
	case "inventory", "archive":
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			receipt, err := transport.Control(ctx, action, stringValue(input.Request["source_sha256"]), stringValue(input.Request["selection_sha256"]))
			if err != nil {
				return nil, err
			}
			if receipt.State == "ready" {
				return map[string]any{"ok": true, "receipt": "ready:" + receipt.SHA256 + ":" + strconv.FormatInt(receipt.Bytes, 10)}, nil
			}
			if receipt.State != "working" {
				return nil, errors.New("native fixture control failed")
			}
			time.Sleep(5 * time.Millisecond)
		}
		return nil, errors.New("native fixture control did not finish")
	case "fetch":
		name := stringValue(input.Request["name"])
		local := "out.tar.gz"
		if name == "inventory.nul" {
			local = name
		}
		if err := copyComputeControlFile(filepath.Join(root, ".synon-harvest", name), filepath.Join(input.StageDirectory, local)); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "ready": true}, nil
	}
	return nil, errors.New("unknown native fixture action")
}
