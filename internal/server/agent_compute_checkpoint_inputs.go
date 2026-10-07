package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"

	"synon-go/internal/compute/checkpoint"
	"synon-go/internal/compute/transfer"
)

type computeInputRecord struct {
	Reader io.ReadCloser
	Raw    any
	SHA256 string
	Bytes  int64
}

var errComputeInputIterationStopped = errors.New("compute input iteration stopped")

func computeInputRecords(ctx context.Context, input map[string]any, resume *computeCheckpointResume, workspaceRoot string) iter.Seq2[computeInputRecord, error] {
	return func(yield func(computeInputRecord, error) bool) {
		for _, raw := range anySliceValue(input["inputs"]) {
			if !yield(computeInputRecord{Raw: raw}, nil) {
				return
			}
		}
		if resume == nil {
			return
		}
		if err := originalCheckpointInputs(ctx, resume, yield); err != nil {
			if !errors.Is(err, errComputeInputIterationStopped) {
				yield(computeInputRecord{}, err)
			}
			return
		}
		manifest, err := os.Open(filepath.Join(resume.Root, filepath.FromSlash(resume.Contract.Manifest)))
		if err != nil {
			yield(computeInputRecord{}, err)
			return
		}
		defer manifest.Close()
		stopped := errors.New("checkpoint iteration stopped")
		receipt, err := checkpoint.Read(ctx, manifest, resume.Receipt.SourceInputSHA256, resume.Contract.CommandSHA256(), func(file checkpoint.File) error {
			source, err := filepath.Rel(workspaceRoot, filepath.Join(resume.Root, filepath.FromSlash(file.Path)))
			if err != nil {
				return err
			}
			if !yield(computeInputRecord{Raw: map[string]any{"src": source, "dst": file.Path}, SHA256: file.SHA256, Bytes: file.Bytes}, nil) {
				return stopped
			}
			return nil
		})
		if errors.Is(err, stopped) {
			return
		}
		if err == nil && receipt != resume.Receipt {
			err = errors.New("checkpoint manifest changed during staging")
		}
		if err != nil {
			yield(computeInputRecord{}, err)
		}
	}
}

func originalCheckpointInputs(ctx context.Context, resume *computeCheckpointResume, yield func(computeInputRecord, error) bool) error {
	manifest, err := os.Open(filepath.Join(resume.Root, filepath.FromSlash(resume.Contract.Manifest)))
	if err != nil {
		return err
	}
	index, err := transfer.NewPathIndex(ctx, filepath.Dir(resume.InputArchive))
	if err != nil {
		_ = manifest.Close()
		return err
	}
	defer index.Close()
	_, err = checkpoint.Read(ctx, manifest, resume.Receipt.SourceInputSHA256, resume.Contract.CommandSHA256(), func(file checkpoint.File) error { return index.Add(ctx, file.Path) })
	closeErr := manifest.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	archive, err := os.Open(resume.InputArchive)
	if err != nil {
		return err
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(transfer.ReaderWithContext(ctx, archive))
	if err != nil {
		return err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return errors.New("original checkpoint input archive contains a non-regular entry")
		}
		if computeControlInputName(header.Name) {
			continue
		}
		replaced, err := index.Contains(ctx, header.Name)
		if err != nil {
			return err
		}
		if replaced {
			continue
		}
		if !yield(computeInputRecord{Raw: map[string]any{"src": "immutable-input:" + header.Name, "dst": header.Name}, Reader: io.NopCloser(reader), Bytes: header.Size}, nil) {
			return errComputeInputIterationStopped
		}
	}
}

func computeControlInputName(name string) bool {
	root := strings.SplitN(name, "/", 2)[0]
	switch root {
	case "_operon_wrapper.sh", "run.sh", ".job_env", "_synon_checkpoint.env", "_synon_harvest.sh", ".synon-harvest", ".phase", ".submit_intent", ".scheduler_id", ".wrapper_identity", ".wrapper_pid", ".launch.lock", ".deadline_fired", ".deadline_termed", ".job_timeout_fired":
		return true
	default:
		return false
	}
}
