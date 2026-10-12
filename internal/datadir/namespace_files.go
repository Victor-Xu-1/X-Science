package datadir

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const namespaceFileLimit = 4 * 1024 * 1024

type namespaceSnapshot struct {
	info os.FileInfo
	raw  []byte
	hash [32]byte
}

// Namespace upgrades accept only a same-owner, unaliased regular source. The
// snapshot is rechecked immediately before publication; recovery never reads
// credentials or alters the contents of the data-directory pointer.
func namespaceFile(path string) (namespaceSnapshot, error) {
	var result namespaceSnapshot
	if _, err := canonicalExistingDirectory(filepath.Dir(path)); err != nil {
		return result, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() > namespaceFileLimit {
		return result, errors.New("namespace source is not a bounded regular file")
	}
	if err := namespaceOwned(path, info, true); err != nil {
		return result, err
	}
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return result, errors.New("namespace source changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, namespaceFileLimit+1))
	if err != nil || len(raw) > namespaceFileLimit {
		return result, errors.New("namespace source cannot be read within its bound")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return result, errors.New("namespace source changed while reading")
	}
	return namespaceSnapshot{info: after, raw: raw, hash: sha256.Sum256(raw)}, nil
}

func (snapshot namespaceSnapshot) unchanged(path string) error {
	current, err := namespaceFile(path)
	if err != nil || !os.SameFile(snapshot.info, current.info) || snapshot.hash != current.hash {
		return errors.New("namespace source no longer matches its verified snapshot")
	}
	return nil
}

func namespaceRecovery(path string, snapshot namespaceSnapshot) error {
	return publishNamespaceRecovery(path, snapshot, func(file *os.File, raw []byte) error {
		_, err := file.Write(raw)
		return err
	})
}

// A recovery identity is published only after its entire private source copy
// is synced and read back. Interrupted staging cannot occupy the final hash
// name and block the original, still-active pointer's next startup.
func publishNamespaceRecovery(path string, snapshot namespaceSnapshot, write func(*os.File, []byte) error) error {
	if existing, err := namespaceFile(path); err == nil {
		if bytes.Equal(existing.raw, snapshot.raw) {
			return nil
		}
		return errors.New("namespace recovery file conflicts with the original snapshot")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if write == nil {
		return errors.New("namespace recovery writer is required")
	}
	if _, err := canonicalExistingDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".namespace-recovery-staging-")
	if err != nil {
		return err
	}
	staging := file.Name()
	owned, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	defer func() {
		_ = file.Close()
		// Never reclaim another invocation's or a replaced staging file.
		if current, err := os.Lstat(staging); err == nil && os.SameFile(owned, current) && current.Mode().IsRegular() {
			_ = os.Remove(staging)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := write(file, snapshot.raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	readback, err := namespaceFile(staging)
	if err != nil || readback.hash != snapshot.hash {
		return errors.New("namespace recovery readback does not match the original snapshot")
	}
	return namespaceRename(staging, path, readback)
}

func namespaceRename(source, target string, snapshot namespaceSnapshot) error {
	if err := snapshot.unchanged(source); err != nil {
		return err
	}
	if err := namespaceRenameExclusive(source, target); err != nil {
		return fmt.Errorf("publish namespace upgrade without replacing existing state: %w", err)
	}
	readback, err := namespaceFile(target)
	if err != nil || readback.hash != snapshot.hash || !os.SameFile(snapshot.info, readback.info) {
		return errors.New("namespace publication changed; retained state requires recovery review")
	}
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	if filepath.Dir(source) != filepath.Dir(target) {
		return syncDirectory(filepath.Dir(source))
	}
	return nil
}
