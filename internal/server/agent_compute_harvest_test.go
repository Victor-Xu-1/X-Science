package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"synon-go/internal/compute/transfer"
)

type nativeHarvestTransport struct{ root string }

func (t nativeHarvestTransport) Install(ctx context.Context, asset string) error {
	return copyComputeControlFile(asset, filepath.Join(t.root, "_synon_harvest.sh"))
}
func (t nativeHarvestTransport) Control(ctx context.Context, action, source, selection string) (transfer.ControlReceipt, error) {
	args := []string{"--noprofile", "--norc", "-p", filepath.Join(t.root, "_synon_harvest.sh"), action}
	if action == "archive" || action == "checkpoint" {
		args = append(args, source, selection)
	}
	command := exec.CommandContext(ctx, "bash", args...)
	command.Dir = t.root
	data, err := command.CombinedOutput()
	if err != nil {
		return transfer.ControlReceipt{}, fmt.Errorf("helper %w: %s", err, data)
	}
	return transfer.ParseControlReceipt(string(data))
}
func (t nativeHarvestTransport) Select(ctx context.Context, local, source, selected string) error {
	return copyComputeControlFile(local, filepath.Join(t.root, ".synon-harvest", "selection-"+selected+".nul"))
}
func (t nativeHarvestTransport) Fetch(ctx context.Context, remote, local string, receipt transfer.ControlReceipt) error {
	if err := copyComputeControlFile(filepath.Join(t.root, ".synon-harvest", remote), local); err != nil {
		return err
	}
	return transfer.VerifyFile(ctx, local, receipt.SHA256, receipt.Bytes)
}

func TestComputeHarvestActualNativeSelectionAndIntegrityReplay(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native GNU helper is Linux-only")
	}
	_, source, _, _ := runtime.Caller(0)
	server := &Server{runtimeAssetsDir: filepath.Join(filepath.Dir(source), "../../assets/optional")}
	remote, stage, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(remote, "out"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"stdout.log", "stderr.log", "out/result.csv"} {
		if err := os.WriteFile(filepath.Join(remote, name), []byte("a,b\n1,2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	large, err := os.Create(filepath.Join(remote, "out/trajectory.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(40 << 30); err != nil {
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var result computeHarvestResult
	for ctx.Err() == nil {
		var ready bool
		result, ready, err = server.harvestSelectedComputeOutputs(ctx, stage, workspace, "job-0123456789abcdef01234567", map[string]any{"outputs": []any{"*.csv"}}, func(name string) string { return "test://owned/" + name }, nativeHarvestTransport{remote})
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if result.Count != 3 || result.RemoteCount != 1 || len(result.Files) != 3 || result.ManifestSHA256 == "" {
		t.Fatalf("harvest=%#v err=%v", result, ctx.Err())
	}
	if info, err := os.Stat(filepath.Join(stage, "out.tar.gz")); err != nil || info.Size() > 4096 {
		t.Fatal("unselected large payload was packaged", info, err)
	}
	// A published directory alone cannot prove integrity after reconstruction.
	if err := os.WriteFile(filepath.Join(workspace, "hpc/job-0123456789abcdef01234567/out/result.csv"), []byte("x,y\n3,4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.harvestSelectedComputeOutputs(ctx, stage, workspace, "job-0123456789abcdef01234567", map[string]any{"outputs": []any{"*.csv"}}, func(name string) string { return name }, nativeHarvestTransport{remote}); err == nil {
		t.Fatal("changed published output accepted as a valid replay")
	}
}
