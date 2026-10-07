package transfer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const ManifestSchema = "synon.compute-output-manifest.v1"
const manifestPreviewRecords = 32

var modifiedPattern = regexp.MustCompile(`^-?[0-9]{1,20}(?:\.[0-9]{1,12})?$`)

type Record struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Bytes    int64  `json:"bytes"`
	Modified string `json:"modified"`
	Selected bool   `json:"selected"`
	Reason   string `json:"reason,omitempty"`
}

// Capacity is a current, caller-admitted target budget, not a dataset limit.
// Unknown byte capacity cannot be represented as unlimited capacity.
type Capacity struct {
	BytesKnown bool
	Bytes      int64
	FilesKnown bool
	Files      int64
}

type Selection struct {
	Schema           string   `json:"schema"`
	SourceSHA256     string   `json:"source_sha256"`
	PolicySHA256     string   `json:"policy_sha256"`
	ManifestSHA256   string   `json:"manifest_sha256"`
	ObservedFiles    int64    `json:"observed_files"`
	SelectedFiles    int64    `json:"selected_files"`
	SelectedBytes    int64    `json:"selected_bytes"`
	RemoteFiles      int64    `json:"remote_files"`
	ExcludedFiles    int64    `json:"excluded_files"`
	UnsupportedFiles int64    `json:"unsupported_files"`
	RemotePreview    []Record `json:"remote_preview"`
}

// ReadRecords consumes the four NUL-separated find fields without collecting
// the inventory in memory. The token bound limits one path, never file count.
func ReadRecords(ctx context.Context, input io.Reader, consume func(Record) error) error {
	if ctx == nil || input == nil || consume == nil {
		return errors.New("output inventory reader authority is missing")
	}
	reader := bufio.NewReaderSize(input, 64<<10)
	for {
		kind, err := manifestToken(ctx, reader, 1)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := manifestToken(ctx, reader, 4096)
		if err != nil {
			return fmt.Errorf("output inventory path: %w", err)
		}
		size, err := manifestToken(ctx, reader, 20)
		if err != nil {
			return fmt.Errorf("output inventory size: %w", err)
		}
		modified, err := manifestToken(ctx, reader, 40)
		if err != nil {
			return fmt.Errorf("output inventory modification identity: %w", err)
		}
		bytes, parseErr := strconv.ParseInt(size, 10, 64)
		name = strings.TrimPrefix(name, "./")
		if parseErr != nil || bytes < 0 || len(kind) != 1 || !strings.Contains("fdlbcps", kind) ||
			!modifiedPattern.MatchString(modified) || !validOutputPath(name, kind) {
			return errors.New("output inventory record is invalid")
		}
		if err := consume(Record{Path: name, Kind: kind, Bytes: bytes, Modified: modified}); err != nil {
			return err
		}
	}
}

func manifestToken(ctx context.Context, reader *bufio.Reader, limit int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	part, err := reader.ReadSlice(0)
	if err == io.EOF && len(part) == 0 {
		return "", io.EOF
	}
	if err != nil || len(part) < 2 || len(part)-1 > limit {
		return "", errors.New("output inventory token is incomplete or oversized")
	}
	return string(part[:len(part)-1]), nil
}

func validOutputPath(name, kind string) bool {
	if !utf8.ValidString(name) || name == "" || path.Clean(name) != name || path.IsAbs(name) {
		return false
	}
	if name == "out" {
		return kind == "d"
	}
	return name == "stdout.log" || name == "stderr.log" || strings.HasPrefix(name, "out/")
}

// BuildSelection writes the complete selected/remote JSONL manifest and an
// exact NUL file list for native tar. It validates the source observation hash
// before returning success. File payloads are not read or marked verified here.
// Excluded names stay private in the source observation, not the public manifest.
func BuildSelection(ctx context.Context, input io.Reader, sourceSHA string, policy Policy, capacity Capacity,
	manifest, selected io.Writer, privateDirectory string,
) (summary Selection, resultErr error) {
	decoded, err := hex.DecodeString(sourceSHA)
	if err != nil || len(decoded) != sha256.Size || len(sourceSHA) != sha256.Size*2 ||
		!capacity.BytesKnown || capacity.Bytes < 0 || (capacity.FilesKnown && capacity.Files < 0) || manifest == nil || selected == nil {
		return summary, errors.New("output selection capacity or observation identity is invalid")
	}
	selector, err := policy.Compile()
	if err != nil {
		return summary, err
	}
	index, err := NewPathIndex(ctx, privateDirectory)
	if err != nil {
		return summary, err
	}
	defer func() { resultErr = errors.Join(resultErr, index.Close()) }()
	policyBytes, err := json.Marshal(policy)
	if err != nil {
		return summary, err
	}
	policyDigest := sha256.Sum256(policyBytes)
	summary = Selection{Schema: ManifestSchema, SourceSHA256: sourceSHA, PolicySHA256: hex.EncodeToString(policyDigest[:]), RemotePreview: []Record{}}
	outputDigest, sourceDigest := sha256.New(), sha256.New()
	previewBytes := 0
	encoder := json.NewEncoder(io.MultiWriter(manifest, outputDigest))
	if err := encoder.Encode(map[string]string{"schema": ManifestSchema, "source_sha256": sourceSHA, "policy_sha256": summary.PolicySHA256}); err != nil {
		return summary, err
	}
	err = ReadRecords(ctx, io.TeeReader(input, sourceDigest), func(record Record) error {
		if err := index.Add(ctx, record.Path); err != nil {
			return err
		}
		if record.Kind == "d" {
			return nil
		}
		if summary.ObservedFiles == math.MaxInt64 {
			return errors.New("output inventory counter overflow")
		}
		summary.ObservedFiles++
		required := false
		if policy.RequiredPaths != nil {
			var err error
			required, err = policy.RequiredPaths.Contains(ctx, record.Path)
			if err != nil {
				return err
			}
		}
		record.Selected, record.Reason = selector.selectPath(record.Path, record.Bytes, summary.SelectedBytes, required)
		if record.Reason == "excluded" {
			summary.ExcludedFiles++
			return nil
		}
		if record.Kind != "f" {
			record.Selected, record.Reason = false, "non_regular"
			summary.UnsupportedFiles++
		} else if record.Selected && (record.Bytes > capacity.Bytes-summary.SelectedBytes ||
			(capacity.FilesKnown && summary.SelectedFiles >= capacity.Files)) {
			record.Selected, record.Reason = false, "storage_capacity"
		}
		if record.Selected {
			if _, err := io.WriteString(selected, record.Path+"\x00"); err != nil {
				return err
			}
			summary.SelectedFiles++
			summary.SelectedBytes += record.Bytes
		} else {
			summary.RemoteFiles++
			encoded, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if len(summary.RemotePreview) < manifestPreviewRecords && len(encoded) <= 32*1024-previewBytes {
				summary.RemotePreview = append(summary.RemotePreview, record)
				previewBytes += len(encoded)
			}
		}
		return encoder.Encode(record)
	})
	if err != nil {
		return summary, err
	}
	if hex.EncodeToString(sourceDigest.Sum(nil)) != sourceSHA {
		return summary, errors.New("output inventory digest changed")
	}
	summary.ManifestSHA256 = hex.EncodeToString(outputDigest.Sum(nil))
	return summary, nil
}
