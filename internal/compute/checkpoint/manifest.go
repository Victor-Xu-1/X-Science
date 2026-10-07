package checkpoint

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"unicode/utf8"
)

type Header struct {
	Schema              string `json:"schema"`
	Generation          int64  `json:"generation"`
	SourceInputSHA256   string `json:"source_input_sha256"`
	ResumeCommandSHA256 string `json:"resume_command_sha256"`
}
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Commit struct {
	Commit    string `json:"commit"`
	FileCount int64  `json:"file_count"`
	Bytes     int64  `json:"bytes"`
}
type Receipt struct {
	Header
	ManifestSHA256 string `json:"manifest_sha256"`
	FileCount      int64  `json:"file_count"`
	Bytes          int64  `json:"bytes"`
}

func validSHA(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && len(value) == 64 && value == hex.EncodeToString(data)
}

// Read consumes bounded JSONL records, not a manifest-sized array. A final
// commit record is mandatory, so a partial checkpoint cannot become runnable.
func Read(ctx context.Context, source io.Reader, expectedInput, expectedCommand string, consume func(File) error) (Receipt, error) {
	var result Receipt
	if ctx == nil || source == nil || !validSHA(expectedInput) || !validSHA(expectedCommand) {
		return result, errors.New("checkpoint observation authority is invalid")
	}
	digest := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(source, digest))
	scanner.Buffer(make([]byte, 16<<10), 16<<10)
	if !scanner.Scan() || decodeRecord(scanner.Bytes(), &result.Header) != nil || result.Schema != Schema || result.Generation < 1 || result.SourceInputSHA256 != expectedInput || result.ResumeCommandSHA256 != expectedCommand {
		return result, errors.New("checkpoint identity header does not match the submitted workload")
	}
	committed := false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if committed {
			return result, errors.New("checkpoint contains data after its commit")
		}
		var commit Commit
		if err := json.Unmarshal(scanner.Bytes(), &commit); err != nil {
			return result, err
		}
		if commit.Commit != "" {
			if err := decodeRecord(scanner.Bytes(), &commit); err != nil {
				return result, err
			}
			if commit.Commit != "complete" || commit.FileCount != result.FileCount || commit.Bytes != result.Bytes || result.FileCount == 0 {
				return result, errors.New("checkpoint commit does not cover its complete file inventory")
			}
			committed = true
			continue
		}
		var file File
		if decodeRecord(scanner.Bytes(), &file) != nil || !ValidPath(file.Path) || !validSHA(file.SHA256) || file.Bytes < 0 || file.Bytes > math.MaxInt64-result.Bytes || result.FileCount == math.MaxInt64 {
			return result, errors.New("checkpoint file record invalid")
		}
		if consume != nil {
			if err := consume(file); err != nil {
				return result, err
			}
		}
		result.FileCount++
		result.Bytes += file.Bytes
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	if !committed {
		return result, errors.New("checkpoint has no complete native commit")
	}
	result.ManifestSHA256 = hex.EncodeToString(digest.Sum(nil))
	return result, nil
}

func decodeRecord(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return errors.New("checkpoint record is not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("checkpoint record must be an object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return errors.New("checkpoint record contains duplicate fields")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("checkpoint record has trailing data")
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
