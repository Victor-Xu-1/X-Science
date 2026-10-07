package transfer

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Extraction struct {
	Files []string `json:"files"`
	Count int64    `json:"count"`
	Bytes int64    `json:"bytes"`
}

// ExtractSelected admits exactly the digest-bound selection, not arbitrary
// decompressed bytes. It checks replayed payloads, never trusts an existing
// directory as proof, and atomically publishes only a complete extraction.
func ExtractSelected(ctx context.Context, archivePath, manifestPath, target string, selection Selection) (Extraction, error) {
	result := Extraction{Files: []string{}}
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() {
		return result, errors.New("output manifest unavailable")
	}
	if err := VerifyFile(ctx, manifestPath, selection.ManifestSHA256, manifestInfo.Size()); err != nil {
		return result, err
	}
	manifest, err := os.Open(manifestPath)
	if err != nil {
		return result, err
	}
	defer manifest.Close()
	scanner := bufio.NewScanner(contextReader{ctx, manifest})
	scanner.Buffer(make([]byte, 16<<10), 16<<10)
	if !scanner.Scan() {
		return result, errors.New("output manifest header missing")
	}
	header := map[string]string{}
	if json.Unmarshal(scanner.Bytes(), &header) != nil || header["schema"] != ManifestSchema ||
		header["source_sha256"] != selection.SourceSHA256 || header["policy_sha256"] != selection.PolicySHA256 {
		return result, errors.New("output manifest identity mismatch")
	}
	nextSelected := func() (Record, error) {
		for scanner.Scan() {
			var record Record
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil || !validOutputPath(record.Path, record.Kind) {
				return Record{}, errors.New("output manifest record invalid")
			}
			if record.Selected {
				if record.Kind != "f" || record.Bytes < 0 {
					return Record{}, errors.New("output manifest selected a non-regular payload")
				}
				return record, nil
			}
		}
		if err := scanner.Err(); err != nil {
			return Record{}, err
		}
		return Record{}, io.EOF
	}
	if filepath.Clean(target) != target || !filepath.IsAbs(target) {
		return result, errors.New("output target invalid")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return result, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil || parent != filepath.Dir(target) {
		return result, errors.New("output target parent changed")
	}
	existing := false
	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return result, errors.New("output target is not a regular directory")
		}
		existing = true
	} else if !os.IsNotExist(err) {
		return result, err
	}
	destinationRoot := target
	if !existing {
		destinationRoot, err = os.MkdirTemp(parent, ".compute-delivery-")
		if err != nil {
			return result, err
		}
		defer os.RemoveAll(destinationRoot)
	}
	source, err := os.Open(archivePath)
	if err != nil {
		return result, err
	}
	defer source.Close()
	compressed, err := gzip.NewReader(contextReader{ctx, source})
	if err != nil {
		return result, err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	previewBytes := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, err
		}
		record, err := nextSelected()
		if err != nil || header.Name != record.Path || header.Size != record.Bytes || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) {
			return result, errors.New("output archive differs from the exact selected manifest")
		}
		destination := filepath.Join(destinationRoot, filepath.FromSlash(record.Path))
		if existing {
			resolved, err := filepath.EvalSymlinks(destination)
			info, statErr := os.Lstat(destination)
			if err != nil || statErr != nil || resolved != destination || !info.Mode().IsRegular() || info.Size() != record.Bytes {
				return result, errors.New("published output changed before replay")
			}
			local, err := os.Open(destination)
			if err != nil {
				return result, err
			}
			localHash, archiveHash := sha256.New(), sha256.New()
			_, localErr := io.Copy(localHash, contextReader{ctx, local})
			_, archiveErr := io.CopyN(archiveHash, reader, record.Bytes)
			closeErr := local.Close()
			if errors.Join(localErr, archiveErr, closeErr) != nil || string(localHash.Sum(nil)) != string(archiveHash.Sum(nil)) {
				return result, errors.New("published output payload changed before replay")
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return result, err
			}
			output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return result, err
			}
			written, copyErr := io.CopyN(StorageWriter{Writer: output, Directory: parent, Reserve: 64 << 20}, reader, record.Bytes)
			closeErr := errors.Join(output.Sync(), output.Close())
			if copyErr != nil || closeErr != nil || written != record.Bytes {
				return result, errors.New("output extraction did not commit the complete payload")
			}
		}
		result.Count++
		result.Bytes += record.Bytes // The complete selection was already overflow-checked.
		encoded, err := json.Marshal(record.Path)
		if err != nil {
			return result, err
		}
		if len(result.Files) < 200 && len(encoded) <= 32*1024-previewBytes {
			result.Files = append(result.Files, record.Path)
			previewBytes += len(encoded)
		}
	}
	if _, err := nextSelected(); err != io.EOF || result.Count != selection.SelectedFiles || result.Bytes != selection.SelectedBytes {
		return result, errors.New("output archive did not contain the complete selection")
	}
	// tar EOF is before gzip's trailer: drain only the bounded framing so CRC
	// or a concatenated decompression bomb cannot hide after the last entry.
	if tail, err := io.CopyN(io.Discard, compressed, 1025); (err != io.EOF && err != nil) || tail > 1024 {
		return result, errors.New("output archive has invalid trailing data")
	}
	if !existing {
		if err := os.Rename(destinationRoot, target); err != nil {
			return result, fmt.Errorf("publish output extraction: %w", err)
		}
	}
	return result, nil
}
