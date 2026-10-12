package productstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func checkPath(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("private state path escaped its authority")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() && !info.Mode().IsRegular() {
				return errors.New("private state contains a link or special node")
			}
			if err := checkOwner(current, info); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if current == root {
			break
		}
	}
	return nil
}

func requireDirectory(root, path string) error {
	if err := checkPath(root, path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return errors.New("private state directory is unavailable")
	}
	return nil
}

func treeSHA256(ctx context.Context, root, directory string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := checkPath(root, path); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(directory, path)
		ownership, err := ownershipFacts(path, info)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%d\x00%d\x00", filepath.ToSlash(relative), ownership, info.Mode(), func() int64 {
			if info.Mode().IsRegular() {
				return info.Size()
			}
			return 0
		}())
		if info.Mode().IsRegular() {
			file, err := openOwnedFile(path)
			if err != nil {
				return err
			}
			defer file.Close()
			buffer := make([]byte, 64*1024)
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				count, err := file.Read(buffer)
				hash.Write(buffer[:count])
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyOwnedTree(ctx context.Context, root, source, target string) error {
	directories := []struct {
		path string
		mode os.FileMode
	}{}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := checkPath(root, path); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(source, path)
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			if relative != "." {
				if err := os.Mkdir(destination, 0o700); err != nil {
					return err
				}
			}
			directories = append(directories, struct {
				path string
				mode os.FileMode
			}{destination, info.Mode().Perm()})
			return nil
		}
		reader, err := openOwnedFile(path)
		if err != nil {
			return err
		}
		defer reader.Close()
		writer, err := createOwnedFile(destination, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, reader)
		syncErr := writer.Sync()
		closeErr := writer.Close()
		return errors.Join(copyErr, syncErr, closeErr, os.Chmod(destination, info.Mode().Perm()))
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		directory := directories[index]
		if err := os.Chmod(directory.path, directory.mode); err != nil {
			return err
		}
		if err := syncDirectory(directory.path); err != nil {
			return err
		}
	}
	return nil
}

func readOwnedFile(path string) ([]byte, error) {
	file, err := openOwnedFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, 65537))
}
func writePrivateFile(path string, raw []byte, mode os.FileMode) error {
	file, err := createOwnedFile(path, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	return errors.Join(writeErr, file.Sync(), file.Close())
}
