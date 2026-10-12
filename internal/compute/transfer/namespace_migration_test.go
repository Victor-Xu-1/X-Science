package transfer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"x-science/internal/compute/namespace"
)

func TestNamespaceOutputExtractionRequiresProofAndActualOriginalManifest(t *testing.T) {
	source := inventoryRecord("f", "out/result.txt", 4)
	selection, manifest, _, err := selectInventory(t, source, Policy{}, Capacity{BytesKnown: true, Bytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	previous := strings.Replace(manifest, ManifestSchema, previousNamespaceManifestSchema, 1)
	selection.ManifestSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(previous)))
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.jsonl")
	archivePath := filepath.Join(root, "payload.tar.gz")
	if err := os.WriteFile(manifestPath, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	writer := tar.NewWriter(gzipWriter)
	if err := writer.WriteHeader(&tar.Header{Name: "out/result.txt", Mode: 0o600, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "delivered")
	if _, err := ExtractSelected(context.Background(), archivePath, manifestPath, target, selection); err == nil {
		t.Fatal("normal new job accepted old output schema")
	}
	inputSHA := strings.Repeat("a", 64)
	if _, err := ExtractSelectedWithHistoricalProof(context.Background(), archivePath, manifestPath, target, selection, inputSHA, namespace.SourceProof{}); err == nil {
		t.Fatal("unconfirmed old job extracted output")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("unconfirmed output created target")
	}
	binding := namespace.JobBinding{JobID: "old-job", OwnerUserID: "owner", ProjectID: "project", Environment: "env", Provider: "ssh", ExternalID: "actual-remote-job", InputSHA256: inputSHA, SourceSHA256: strings.Repeat("b", 64)}
	proof, err := namespace.Observe(context.Background(), binding, func(context.Context, string, string) (namespace.JobBinding, bool, error) { return binding, true, nil })
	if err != nil {
		t.Fatal(err)
	}
	sourceProof, err := proof.BindSource(namespace.SourceBinding{JobID: binding.JobID, Environment: binding.Environment, Provider: binding.Provider, ExternalID: binding.ExternalID, ManifestSHA256: selection.ManifestSHA256})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExtractSelectedWithHistoricalProof(context.Background(), archivePath, manifestPath, target, selection, inputSHA, sourceProof)
	if err != nil || result.Count != 1 {
		t.Fatalf("verified historical extraction failed: %#v %v", result, err)
	}
	if actual, err := os.ReadFile(filepath.Join(target, "out/result.txt")); err != nil || string(actual) != "data" {
		t.Fatal("actual delivered bytes differ")
	}
	if actual, err := os.ReadFile(manifestPath); err != nil || string(actual) != previous {
		t.Fatal("original manifest was rewritten")
	}
	if _, err := ExtractSelectedWithHistoricalProof(context.Background(), archivePath, manifestPath, filepath.Join(root, "different"), selection, strings.Repeat("c", 64), sourceProof); err == nil {
		t.Fatal("different input reused historical source")
	}
}
