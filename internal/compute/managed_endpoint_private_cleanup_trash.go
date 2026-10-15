package compute

import (
	"context"
	"errors"
	"io"
	"os"
)

func removeManagedEndpointPrivateCleanupTrash(ctx context.Context, root *os.Root, parent *os.File, trash string, expected os.FileInfo, runtimeRoot string, scope ManagedEndpointPrivateCleanup) error {
	// Commit the rename before removing any contents, including a retry after
	// an earlier rename Sync failed. The trash name alone is not authority.
	if err := parent.Sync(); err != nil {
		return err
	}
	directory, err := root.OpenRoot(trash)
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.Open(".")
	if err != nil {
		return err
	}
	defer entries.Close()
	opened, err := entries.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(expected, opened) {
		return errors.New("managed endpoint private cleanup trash changed while opening")
	}
	owner, err := validateManagedEndpointPrivateCleanupOwnerDirectory(directory, runtimeRoot, scope)
	if errors.Is(err, os.ErrNotExist) {
		remaining, readErr := entries.ReadDir(1)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if len(remaining) != 0 {
			return errors.New("managed endpoint private cleanup nonempty trash has no owner descriptor")
		}
		return finishManagedEndpointPrivateCleanupTrash(root, parent, trash, opened, entries)
	}
	if err != nil {
		return err
	}
	if err := removeManagedEndpointPrivateCleanupContents(ctx, directory, entries); err != nil {
		return err
	}
	// Preserve the descriptor until all other removals are durable. A crash
	// before this Sync must leave exact authority available for the next retry.
	if err := entries.Sync(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if err := managedEndpointPrivateCleanupOwnerIsLast(directory, owner); err != nil {
		return err
	}
	if err := directory.Remove("owner.json"); err != nil {
		return err
	}
	return finishManagedEndpointPrivateCleanupTrash(root, parent, trash, opened, entries)
}

func removeManagedEndpointPrivateCleanupContents(ctx context.Context, directory *os.Root, entries *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		batch, readErr := entries.ReadDir(32)
		for _, entry := range batch {
			if entry.Name() == "owner.json" {
				continue
			}
			if err := ctx.Err(); err != nil {
				return context.Cause(ctx)
			}
			// Every recursive operation uses the pinned nested root, never the
			// replaceable trash name in the parent directory.
			if err := directory.RemoveAll(entry.Name()); err != nil {
				return errors.Join(err, entries.Sync())
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return errors.Join(readErr, entries.Sync())
		}
	}
}

func managedEndpointPrivateCleanupOwnerIsLast(directory *os.Root, owner os.FileInfo) error {
	remaining, err := directory.Open(".")
	if err != nil {
		return err
	}
	defer remaining.Close()
	batch, err := remaining.ReadDir(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(batch) != 1 || batch[0].Name() != "owner.json" {
		return errors.New("managed endpoint private cleanup trash changed during removal")
	}
	current, err := directory.Lstat("owner.json")
	if err != nil || !os.SameFile(owner, current) {
		return errors.New("managed endpoint private cleanup owner descriptor changed during removal")
	}
	return nil
}

func finishManagedEndpointPrivateCleanupTrash(root *os.Root, parent *os.File, trash string, expected os.FileInfo, directory *os.File) error {
	// This Sync also covers an empty retry after owner.json was removed but
	// its prior Sync failed. Never acknowledge deletion before it is durable.
	if err := directory.Sync(); err != nil {
		return err
	}
	current, err := root.Lstat(trash)
	if err != nil || !os.SameFile(expected, current) {
		return errors.New("managed endpoint private cleanup trash changed before removal")
	}
	// Nonrecursive Remove cannot delete files in a replacement or newly
	// nonempty directory, unlike RemoveAll against the parent's trash name.
	removeErr := root.Remove(trash)
	return errors.Join(removeErr, parent.Sync())
}
