//go:build linux

package kernel

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProviderTransferNativeResumeIntegrityAndStageAuthority(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-I", "-B", filepath.Join(root, "internal/kernel/testdata/compute_transfer_protocol.py"), filepath.Join(root, "assets/optional/compute"), "-v")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native provider protocol: %v\n%s", err, output)
	}
}
