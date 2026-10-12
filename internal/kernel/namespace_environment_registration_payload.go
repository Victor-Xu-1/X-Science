package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Stream every physical file in bounded chunks, without copying the prefix or
// making dataset-sized buffers. Symlinks must stay within this exact prefix.
func namespacePrefixPayloadDigest(ctx context.Context, prefix string) (string, []namespaceEnvironmentUnresolvedDependency, error) {
	root, err := os.OpenRoot(prefix)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	digest := sha256.New()
	buffer := make([]byte, 128<<10)
	dependencies := []namespaceEnvironmentUnresolvedDependency(nil)
	_, _ = io.WriteString(digest, "x-science.environment-physical-prefix.v1\x00")
	err = filepath.WalkDir(prefix, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(prefix, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		_, _ = io.WriteString(digest, filepath.ToSlash(relative)+"\x00"+info.Mode().String()+"\x00")
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				dependency, observeErr := observeNamespaceUnresolvedDependency(prefix, relative, target, err)
				if observeErr != nil {
					return observeErr
				}
				dependencies = append(dependencies, dependency)
			} else if !hostPathContains(prefix, resolved) {
				return errors.New("physical environment symlink escapes its original prefix")
			}
			_, _ = io.WriteString(digest, target+"\x00")
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("physical environment contains an unsupported special file")
		}
		file, err := root.Open(relative)
		if err != nil {
			return err
		}
		chunk := sha256.New()
		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return err
			}
			n, readErr := file.Read(buffer)
			_, _ = chunk.Write(buffer[:n])
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = file.Close()
				return readErr
			}
		}
		after, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil || closeErr != nil || after.Size() != info.Size() || after.ModTime() != info.ModTime() {
			return errors.New("physical environment file changed during observation")
		}
		_, _ = io.WriteString(digest, hex.EncodeToString(chunk.Sum(nil))+"\x00")
		return nil
	})
	return hex.EncodeToString(digest.Sum(nil)), dependencies, err
}
