//go:build linux

package transfer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeHarvestSelectsBeforeBundlingAndReplaysTheSameReceipt(t *testing.T) {
	root := t.TempDir()
	_, testFile, _, _ := runtime.Caller(0)
	helperBody, err := os.ReadFile(filepath.Join(filepath.Dir(testFile), "../../../assets/optional/compute/harvest.sh.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "_synon_harvest.sh")
	if err := os.WriteFile(helper, helperBody, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "out"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"stdout.log", "stderr.log"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "out/table.csv"), []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	large, err := os.Create(filepath.Join(root, "out/large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(40 << 30); err != nil {
		_ = large.Close()
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) (ControlReceipt, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		arguments := append([]string{"--noprofile", "--norc", "-p", helper}, args...)
		command := exec.CommandContext(ctx, "bash", arguments...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			return ControlReceipt{}, fmt.Errorf("native helper: %w: %s", err, output)
		}
		return ParseControlReceipt(string(output))
	}
	wait := func(args ...string) ControlReceipt {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			receipt, err := call(args...)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.State == "ready" {
				return receipt
			}
			if receipt.State != "working" {
				t.Fatalf("unexpected native state: %#v", receipt)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("selected small output was blocked by an unselected large payload")
		return ControlReceipt{}
	}
	observation := wait("inventory")
	payload, err := os.ReadFile(filepath.Join(root, ".synon-harvest/inventory.nul"))
	if err != nil || observation.SHA256 != fmt.Sprintf("%x", sha256.Sum256(payload)) || observation.Bytes != int64(len(payload)) {
		t.Fatalf("native observation identity mismatch: %#v %v", observation, err)
	}
	var selected, manifest bytes.Buffer
	selection, err := BuildSelection(context.Background(), bytes.NewReader(payload), observation.SHA256,
		Policy{Outputs: []Output{{Glob: "*.csv"}}}, Capacity{BytesKnown: true, Bytes: 4096}, &manifest, &selected, root)
	if err != nil || selection.RemoteFiles != 1 || selection.SelectedFiles != 3 {
		t.Fatalf("native selection = %#v %v", selection, err)
	}
	selectionSHA := fmt.Sprintf("%x", sha256.Sum256(selected.Bytes()))
	if err := os.WriteFile(filepath.Join(root, ".synon-harvest/selection-"+selectionSHA+".nul"), selected.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	packaged := wait("archive", observation.SHA256, selectionSHA)
	archiveName := filepath.Join(root, ".synon-harvest/archive-"+observation.SHA256+"-"+selectionSHA+".tar.gz")
	archive, err := os.ReadFile(archiveName)
	if err != nil || len(archive) > 4096 || packaged.SHA256 != fmt.Sprintf("%x", sha256.Sum256(archive)) {
		t.Fatalf("selected archive copied the large payload or lost integrity: bytes=%d receipt=%#v err=%v", len(archive), packaged, err)
	}
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	names := []string{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	if len(names) != 3 || strings.Contains(strings.Join(names, "\n"), "large.bin") {
		t.Fatalf("archive membership differs from the exact selected list: %v", names)
	}
	before, _ := os.Stat(archiveName)
	replayed := wait("archive", observation.SHA256, selectionSHA)
	after, _ := os.Stat(archiveName)
	if replayed != packaged || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("control replay regenerated a previously accepted archive")
	}
}

func TestNativeHarvestRejectsControlSymlinksWithoutReadingTheirTargets(t *testing.T) {
	root := t.TempDir()
	_, testFile, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(testFile), "../../../assets/optional/compute/harvest.sh.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "_synon_harvest.sh")
	if err := os.WriteFile(helper, body, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".synon-harvest"), 0o700); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(t.TempDir(), "private-fixture")
	const contents = "private-test-content-must-not-be-read"
	if err := os.WriteFile(private, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, filepath.Join(root, ".synon-harvest/inventory.receipt")); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", "--noprofile", "--norc", "-p", helper, "inventory")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err == nil || strings.Contains(string(output), contents) {
		t.Fatalf("a control symlink was followed: err=%v output=%q", err, output)
	}
	retained, err := os.ReadFile(private)
	if err != nil || string(retained) != contents {
		t.Fatal("unrelated fixture was overwritten")
	}
}
