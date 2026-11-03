package skills

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifestWithLargeMetadata(t *testing.T, path, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("---\nname: " + name + "\ndescription: Valid small-body workflow\n"); err != nil {
		t.Fatal(err)
	}
	comment := "# " + strings.Repeat("x", 64<<10) + "\n"
	for range 176 {
		if _, err := file.WriteString(comment); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := file.WriteString("---\nA small valid body.\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultManifestBoundaryKeepsValidSibling(t *testing.T) {
	directory := t.TempDir()
	writeManifestWithLargeMetadata(t, filepath.Join(directory, "oversize", "SKILL.md"), "oversize-metadata")
	validPath := filepath.Join(directory, "retained", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(validPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validPath, []byte("---\nname: retained-workflow\ndescription: Kept workflow\n---\nSmall valid body.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Exercise the existing no-option entry. The body is small; oversized
	// frontmatter must not evade the finite whole-manifest read boundary.
	catalog := LoadFromRoot(root, directory)
	items := catalog.Skills()
	if len(items) != 1 || items[0].Name != "retained-workflow" {
		t.Fatalf("catalog accepted oversized metadata or lost valid sibling: count=%d", len(items))
	}
	if len(catalog.LoadErrors()) != 1 {
		t.Fatalf("oversized manifest diagnostics = %d, want one", len(catalog.LoadErrors()))
	}
}

func TestManifestReadBoundaryOptionsAndBodyRemainIndependent(t *testing.T) {
	directory := t.TempDir()
	manifest := "---\nname: bounded-workflow\ndescription: Valid workflow\n---\nOriginal complete body.\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, limit := range []int64{-1, 1<<63 - 1} {
		catalog, err := LoadFromRootContext(context.Background(), root, directory, LoadOptions{MaxManifestBytes: limit})
		if !errors.Is(err, ErrInvalidLoadOptions) || len(catalog.Skills()) != 0 {
			t.Errorf("invalid manifest limit %d: error=%v skills=%d", limit, err, len(catalog.Skills()))
		}
	}
	if _, err := LoadFromRootContext(nil, root, directory); !errors.Is(err, ErrNilLoadContext) {
		t.Errorf("nil context = %v", err)
	}
	catalog, err := LoadFromRootContext(context.Background(), root, directory, LoadOptions{
		MaxManifestBytes: int64(len(manifest)), MaxBodyBytes: 8,
	})
	if err != nil || len(catalog.Skills()) != 1 || catalog.Skills()[0].Body != "Original" {
		t.Fatalf("whole file boundary changed legacy body truncation: err=%v count=%d", err, len(catalog.Skills()))
	}
	_, err = LoadFromRootContext(context.Background(), root, directory, LoadOptions{
		MaxManifestBytes: int64(len(manifest)), MaxBodyBytes: 8, RejectOversizeBody: true,
	})
	if err == nil || !strings.Contains(err.Error(), "body exceeds 8 bytes") {
		t.Fatalf("strict body limit disappeared: %v", err)
	}
	_, err = LoadFromRootContext(context.Background(), root, directory, LoadOptions{MaxManifestBytes: int64(len(manifest) - 1)})
	var tooLarge *ManifestTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.ObservedBytes != int64(len(manifest)) {
		t.Fatalf("whole manifest limit = %v", err)
	}
}

type countedManifestReader struct {
	reader io.Reader
	read   int
	cancel context.CancelFunc
}

func (r *countedManifestReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	r.read += n
	if r.cancel != nil {
		r.cancel()
	}
	return n, err
}

func TestManifestReadGrowthWitnessAndCooperativeCancellation(t *testing.T) {
	reader := &countedManifestReader{reader: strings.NewReader(strings.Repeat("x", 8192))}
	data, err := readSkillManifestBytes(context.Background(), reader, 63)
	var tooLarge *ManifestTooLargeError
	if !errors.As(err, &tooLarge) || data != nil || reader.read != 64 || tooLarge.ObservedBytes != 64 {
		t.Fatalf("growth seam did not stop at limit+1: error=%v reads=%d bytes=%d", err, reader.read, len(data))
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceledReader := &countedManifestReader{reader: strings.NewReader("complete bytes"), cancel: cancel}
	if data, err := readSkillManifestBytes(ctx, canceledReader, 64); !errors.Is(err, context.Canceled) || data != nil {
		t.Fatalf("canceled read returned executable bytes: error=%v bytes=%d", err, len(data))
	}
	neverRead := &countedManifestReader{reader: strings.NewReader("unread")}
	if _, err := readSkillManifestBytes(ctx, neverRead, 64); !errors.Is(err, context.Canceled) || neverRead.read != 0 {
		t.Fatalf("pre-canceled read touched input: error=%v reads=%d", err, neverRead.read)
	}
}

func TestContextManifestLoadReportsPartialAndCancellation(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: valid-before-error\ndescription: valid\n---\nBody."), 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifestWithLargeMetadata(t, filepath.Join(directory, "oversize", "SKILL.md"), "rejected-metadata")
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	catalog, err := LoadFromRootContext(context.Background(), root, directory)
	var tooLarge *ManifestTooLargeError
	if !errors.As(err, &tooLarge) || len(catalog.Skills()) != 1 || len(catalog.LoadErrors()) != 1 {
		t.Fatalf("incomplete context result was not distinguishable: err=%v entries=%d", err, len(catalog.Skills()))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	catalog, err = LoadFromRootContext(ctx, root, directory)
	if !errors.Is(err, context.Canceled) || len(catalog.Skills()) != 0 {
		t.Fatalf("canceled scan = %v entries=%d", err, len(catalog.Skills()))
	}
}

func TestCanceledSkillLoadRejectsEmptyAndUnopenedSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, source := range []struct {
		name        string
		directories []string
	}{
		{name: "empty"},
		{name: "blank", directories: []string{"", " \t\n"}},
	} {
		t.Run(source.name, func(t *testing.T) {
			catalog, err := LoadContext(ctx, source.directories)
			if !errors.Is(err, context.Canceled) || len(catalog.Skills()) != 0 {
				t.Fatalf("canceled source returned success: error=%v entries=%d", err, len(catalog.Skills()))
			}
		})
	}
	t.Run("unopened-root", func(t *testing.T) {
		catalog, err := LoadFromRootContext(ctx, nil, "unopened-source")
		if !errors.Is(err, context.Canceled) || len(catalog.Skills()) != 0 {
			t.Fatalf("root validation preceded cancellation: error=%v entries=%d", err, len(catalog.Skills()))
		}
	})
}
