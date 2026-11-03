package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// LoadFromRoot borrows an already-authorized directory capability. The caller
// retains ownership and closes it. Enumeration and reads share that same root;
// a replacement of the display path cannot select a different filesystem tree.
func LoadFromRoot(root *os.Root, directory string, options ...LoadOptions) *Catalog {
	catalog, _ := LoadFromRootContext(context.Background(), root, directory, options...)
	return catalog
}

// LoadFromRootContext borrows the same authorized root as LoadFromRoot. An
// incomplete result is never suitable for publishing a replacement projection.
func LoadFromRootContext(ctx context.Context, root *os.Root, directory string, options ...LoadOptions) (*Catalog, error) {
	catalog := NewCatalog()
	opts, err := skillLoadOptions(ctx, options)
	if err != nil {
		catalog.AddLoadError(directory, err)
		return catalog, err
	}
	if root == nil {
		err := errors.New("skill root is not open")
		catalog.AddLoadError(directory, err)
		return catalog, err
	}
	err = loadOpenedDirectory(ctx, catalog, directory, root, opts)
	catalog.mu.Lock()
	catalog.sortSkillsLocked()
	catalog.mu.Unlock()
	return catalog, err
}

func skillLoadOptions(ctx context.Context, options []LoadOptions) (LoadOptions, error) {
	if ctx == nil {
		return LoadOptions{}, ErrNilLoadContext
	}
	if err := ctx.Err(); err != nil {
		return LoadOptions{}, err
	}
	opts := LoadOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.MaxManifestBytes == 0 {
		opts.MaxManifestBytes = DefaultMaxManifestBytes
	}
	if opts.MaxManifestBytes < 0 || opts.MaxManifestBytes == 1<<63-1 {
		return LoadOptions{}, ErrInvalidLoadOptions
	}
	return opts, nil
}

// ManifestTooLargeError describes a rejected complete file, not a truncated
// executable contract. ObservedBytes may be only the limit+1 growth witness.
type ManifestTooLargeError struct {
	LimitBytes    int64
	ObservedBytes int64
}

func (e *ManifestTooLargeError) Error() string {
	return fmt.Sprintf("skill manifest exceeds %d-byte read limit (observed at least %d bytes)", e.LimitBytes, e.ObservedBytes)
}

func loadOpenedDirectory(ctx context.Context, catalog *Catalog, directory string, root *os.Root, opts LoadOptions) error {
	initialErrors := len(catalog.LoadErrors())
	var failures []error
	report := func(path string, err error) {
		catalog.AddLoadError(path, err)
		failures = append(failures, err)
	}
	presentation := newPresentationLoader(ctx, root, directory, catalog)
	err := fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(directory, filepath.FromSlash(relative))
		if err != nil {
			report(path, err)
			return nil
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			report(path, errors.New("skill manifest must be a regular file, not a symbolic link"))
			return nil
		}
		if !entry.Type().IsRegular() {
			report(path, errors.New("skill manifest must be a regular file, not a pipe or device"))
			return nil
		}
		data, err := readOpenedSkillManifest(ctx, root, filepath.FromSlash(relative), opts.MaxManifestBytes)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			report(path, err)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		skill, err := parseSkillFile(path, data)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			report(path, err)
			return nil
		}
		presentation.apply(&skill, relative)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		body := skill.Body
		if opts.MaxBodyBytes > 0 && len(body) > opts.MaxBodyBytes {
			if opts.RejectOversizeBody {
				report(path, fmt.Errorf("skill body exceeds %d bytes", opts.MaxBodyBytes))
				return nil
			}
			body = truncateString(body, opts.MaxBodyBytes)
		}
		skill.Body, skill.BodyHash = body, sha1Hex(body)
		catalog.AddSkill(skill)
		return nil
	})
	if err != nil {
		report(directory, err)
	}
	if ctx.Err() == nil {
		presentation.finish()
	}
	if err := ctx.Err(); err != nil && !errors.Is(errors.Join(failures...), err) {
		report(directory, err)
	}
	if len(failures) == 0 {
		if diagnostics := catalog.LoadErrors(); len(diagnostics) > initialErrors {
			failures = append(failures, errors.New(diagnostics[initialErrors].Err))
		}
	}
	return errors.Join(failures...)
}

func readOpenedSkillManifest(ctx context.Context, root *os.Root, relative string, limit int64) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("skill manifest is not a regular file")
	}
	if info.Size() > limit {
		return nil, &ManifestTooLargeError{LimitBytes: limit, ObservedBytes: info.Size()}
	}
	return readSkillManifestBytes(ctx, file, limit)
}

type skillContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r skillContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(data)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

func readSkillManifestBytes(ctx context.Context, reader io.Reader, limit int64) ([]byte, error) {
	if ctx == nil {
		return nil, ErrNilLoadContext
	}
	if limit <= 0 || limit == 1<<63-1 {
		return nil, ErrInvalidLoadOptions
	}
	data, err := io.ReadAll(io.LimitReader(skillContextReader{ctx: ctx, reader: reader}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, &ManifestTooLargeError{LimitBytes: limit, ObservedBytes: int64(len(data))}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
