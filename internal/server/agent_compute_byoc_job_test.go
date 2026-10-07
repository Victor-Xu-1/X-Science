package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"synon-go/internal/compute/transfer"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func TestBYOCInputArchiveRejectsWorkspaceEscapeAndControlEnvironment(t *testing.T) {
	root := repositoryRootForServerTest(t)
	workspaceDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspaceDir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	server := &Server{runtimeAssetsDir: filepath.Join(root, "assets", "optional")}
	for name, input := range map[string]map[string]any{
		"source symlink escape": {
			"command": "true", "inputs": []any{map[string]any{"src": "escape.txt", "dst_filename": "escape.txt"}},
		},
		"destination traversal": {
			"command": "true", "inputs": []any{map[string]any{"src": "escape.txt", "dst_filename": "../escape.txt"}},
		},
		"control environment": {
			"command": "true", "env": map[string]any{"HTTP_PROXY": "http://evil.invalid"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := server.writeAgentBYOCInputArchive(t.TempDir(), workspaceDir, input); err == nil {
				t.Fatalf("unsafe BYOC input was accepted: %#v", input)
			}
		})
	}
}

func TestComputeIngressCancelsBeforeReadingAndKeepsControlNamesReserved(t *testing.T) {
	server := &Server{runtimeAssetsDir: filepath.Join(repositoryRootForServerTest(t), "assets/optional")}
	workspaceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceDir, "input.bin"), []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := server.writeAgentBYOCInputArchiveContext(ctx, t.TempDir(), workspaceDir, map[string]any{"command": "true", "inputs": []any{"input.bin"}}); err == nil {
		t.Fatal("cancelled ingress was staged")
	}
	for _, name := range []string{".phase", ".synon-harvest/selection.nul", "_synon_checkpoint.env", "_synon_harvest.sh"} {
		if _, err := server.writeAgentBYOCInputArchive(t.TempDir(), workspaceDir, map[string]any{"command": "true", "inputs": []any{map[string]any{"src": "input.bin", "dst": name}}}); err == nil {
			t.Fatalf("control input name admitted: %s", name)
		}
	}
}

func TestBYOCHarvestRejectsTraversalAndNonRegularEntries(t *testing.T) {
	for name, header := range map[string]*tar.Header{
		"traversal": {Name: "../escape.txt", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg},
		"symlink":   {Name: "out/link", Mode: 0o777, Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink},
	} {
		t.Run(name, func(t *testing.T) {
			stage := t.TempDir()
			archive, err := os.OpenFile(filepath.Join(stage, "out.tar.gz"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			compressed := gzip.NewWriter(archive)
			writer := tar.NewWriter(compressed)
			if err := writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if header.Typeflag == tar.TypeReg {
				if _, err := writer.Write([]byte("x")); err != nil {
					t.Fatal(err)
				}
			}
			if err := errors.Join(writer.Close(), compressed.Close(), archive.Close()); err != nil {
				t.Fatal(err)
			}
			workspaceDir := t.TempDir()
			observation := []byte("f\x00out/expected.txt\x001\x001.0000000000\x00")
			var manifest, selected bytes.Buffer
			selection, err := transfer.BuildSelection(context.Background(), bytes.NewReader(observation), fmt.Sprintf("%x", sha256.Sum256(observation)), transfer.Policy{}, transfer.Capacity{BytesKnown: true, Bytes: 4096}, &manifest, &selected, stage)
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(stage, "output-manifest.jsonl")
			if err := os.WriteFile(manifestPath, manifest.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := transfer.ExtractSelected(context.Background(), filepath.Join(stage, "out.tar.gz"), manifestPath, filepath.Join(workspaceDir, "job-malicious"), selection); err == nil {
				t.Fatalf("malicious BYOC harvest entry was accepted: %#v", header)
			}
			if _, err := os.Stat(filepath.Join(workspaceDir, "escape.txt")); !os.IsNotExist(err) {
				t.Fatalf("harvest traversal created an outside file: %v", err)
			}
		})
	}
}

func TestBYOCOutputContractSupportsRecursiveFeaturedAndHiddenGlobs(t *testing.T) {
	outputs := []any{
		map[string]any{"glob": "out/**/*.pdb", "visibility": "featured"},
		map[string]any{"glob": "out/**/*.log", "visibility": "hidden"},
	}
	if err := validateAgentBYOCOutputs(outputs); err != nil {
		t.Fatal(err)
	}
	files := []string{
		"hpc/job-fixture/out/ranked.pdb",
		"hpc/job-fixture/out/nested/model.pdb",
		"hpc/job-fixture/out/nested/run.log",
		"hpc/job-fixture/stdout.log",
	}
	featured := featuredComputeProviderFiles(files, outputs)
	if len(featured) != 2 || featured[0] != files[0] || featured[1] != files[1] {
		t.Fatalf("featured files=%v", featured)
	}
	for _, invalid := range []any{
		map[string]any{"glob": "../escape", "visibility": "featured"},
		map[string]any{"glob": "out/*.txt", "visibility": "private"},
		map[string]any{"path": "out/result.txt"},
	} {
		if err := validateAgentBYOCOutputs([]any{invalid}); err == nil {
			t.Fatalf("invalid output was accepted: %#v", invalid)
		}
	}
	policy := transfer.Policy{
		Outputs: []transfer.Output{{Glob: "out/**"}}, Exclude: []string{"out/tmp/**"}, MaxFileBytes: 1024, MaxTotalBytes: 4096,
	}
	selector, err := policy.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if included, reason := selector.Select("out/tmp/cache.bin", 1, 0); included || reason != "excluded" {
		t.Fatalf("excluded output included=%t reason=%q", included, reason)
	}
	if included, reason := selector.Select("out/model.bin", 2048, 0); included || reason != "max_file_bytes" {
		t.Fatalf("over-limit output included=%t reason=%q", included, reason)
	}
}

func TestComputeInputArchiveResolvesTaskOwnedArtifactVersion(t *testing.T) {
	repositoryRoot := repositoryRootForServerTest(t)
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "owner-artifact-input", "project-artifact-input", "frame-artifact-input")
	_, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: "artifact-compute-input", ProjectID: "project-artifact-input",
		Name: "reference.dat", Kind: "data", Content: []byte("artifact-input-evidence"),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := New(Options{
		Workspace: store, Transcript: repo, FileRoot: t.TempDir(),
		RuntimeAssetsDir: filepath.Join(repositoryRoot, "assets", "optional"),
	})
	access, found, err := store.GetKernelFrameAccessContext(context.Background(), "frame-artifact-input")
	if err != nil || !found {
		t.Fatalf("artifact input access found=%t err=%v", found, err)
	}
	stage := t.TempDir()
	if _, err := server.writeAgentBYOCInputArchive(stage, t.TempDir(), map[string]any{
		"command": "true", "inputs": []any{"{{artifact:" + version.ID + "}}"},
	}, access); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(stage, "in.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if header.Name != "reference.dat" {
			continue
		}
		body, readErr := io.ReadAll(reader)
		if readErr != nil || string(body) != "artifact-input-evidence" {
			t.Fatalf("artifact input body=%q err=%v", body, readErr)
		}
		return
	}
	t.Fatal("artifact version was not staged under its canonical filename")
}
