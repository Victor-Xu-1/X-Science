package compute

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// CleanupManagedEndpointPrivateFiles only operates inside the server's current
// trusted root. ServiceDir and WorkspacePath are comparison facts, never targets.
// The outbox intent survives SQL deletion and owns its exact scoped trash name,
// including retries after RemoveAll removed only part of that directory.
func CleanupManagedEndpointPrivateFiles(ctx context.Context, runtimeRoot string, scope ManagedEndpointPrivateCleanup) error {
	if ctx == nil || !filepath.IsAbs(runtimeRoot) {
		return errors.New("managed endpoint private cleanup root is invalid")
	}
	key, err := scope.Key()
	if err != nil {
		return err
	}
	// A captured old root is only a comparison fact. Never acknowledge a move
	// as absence, and never use a payload path to select the filesystem root.
	if scope.RuntimeRoot != runtimeRoot {
		return errors.New("managed endpoint private cleanup does not match the current runtime root")
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	root, err := os.OpenRoot(runtimeRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	trash := ".retired-" + key
	info, sourceErr := root.Lstat(scope.InstanceID)
	retired, trashErr := root.Lstat(trash)
	if trashErr != nil && !errors.Is(trashErr, os.ErrNotExist) {
		return trashErr
	}
	if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
		return sourceErr
	}
	if trashErr == nil {
		if sourceErr == nil || !retired.IsDir() || retired.Mode()&os.ModeSymlink != 0 {
			return errors.New("managed endpoint private cleanup trash is ambiguous")
		}
		return root.RemoveAll(trash)
	}
	if errors.Is(sourceErr, os.ErrNotExist) {
		return nil
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed endpoint private owner directory is not a direct directory")
	}
	if err := validateManagedEndpointPrivateCleanupOwner(root, runtimeRoot, scope); err != nil {
		return err
	}
	current, err := root.Lstat(scope.InstanceID)
	if err != nil || !os.SameFile(info, current) {
		return errors.New("managed endpoint private owner directory changed during validation")
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if err := root.Rename(scope.InstanceID, trash); err != nil {
		return err
	}
	moved, err := root.Lstat(trash)
	if err != nil || !os.SameFile(info, moved) {
		return errors.New("managed endpoint private cleanup rename changed its owner")
	}
	return root.RemoveAll(trash)
}

func validateManagedEndpointPrivateCleanupOwner(root *os.Root, runtimeRoot string, scope ManagedEndpointPrivateCleanup) error {
	directory, err := root.OpenRoot(scope.InstanceID)
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Lstat("owner.json")
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<10 {
		return ErrManagedEndpointCorruptOwner
	}
	file, err := directory.Open("owner.json")
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return ErrManagedEndpointCorruptOwner
	}
	decoder := json.NewDecoder(io.LimitReader(file, 256<<10+1))
	decoder.DisallowUnknownFields()
	var instance ManagedEndpointOwnerInstance
	if err := decoder.Decode(&instance); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrManagedEndpointCorruptOwner
	}
	observed, err := ManagedEndpointPrivateCleanupFor(instance)
	if err != nil {
		return err
	}
	if instance.RuntimeRoot != runtimeRoot || observed != scope {
		return errors.New("managed endpoint private owner descriptor does not match deletion scope")
	}
	return nil
}
