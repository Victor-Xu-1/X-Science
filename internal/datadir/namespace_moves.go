package datadir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// These are exact original controller outputs, not alternate current names.
const previousSourceMarkerPrefix = ".synon-data-move-source-"
const previousCompletionMarker = ".synon-data-move-complete"
const previousStagingInfix = ".synon-migrate-"

type namespaceMoveOperation struct {
	source, target string
	snapshot       namespaceSnapshot
	directory      os.FileInfo
}

// Build and validate the entire plan before changing any names. Only artifacts
// tied to a persisted Move and its exact source marker can enter this plan.
func planMoveNamespace(state State) ([]namespaceMoveOperation, error) {
	var plan []namespaceMoveOperation
	for index, move := range []*Move{state.Pending, state.LastMove} {
		if move == nil || !move.Migrate {
			continue
		}
		if !namespaceMoveID(move.ID) || !filepath.IsAbs(move.Source) || !filepath.IsAbs(move.Target) ||
			filepath.Clean(move.Source) != move.Source || filepath.Clean(move.Target) != move.Target || pathsOverlap(move.Source, move.Target) {
			return nil, errors.New("data move namespace upgrade has an invalid receipt")
		}
		oldSource := filepath.Join(move.Source, previousSourceMarkerPrefix+move.ID)
		newSource := sourceMarkerPath(move.Source, move.ID)
		oldStage := move.Target + previousStagingInfix + move.ID
		legacy := false
		for _, path := range []string{oldSource, filepath.Join(move.Target, previousCompletionMarker), oldStage} {
			if _, err := os.Lstat(path); err == nil {
				legacy = true
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		if !legacy {
			continue
		}
		if _, err := canonicalExistingDirectory(move.Source); err != nil {
			return nil, errors.New("original move source changed before namespace upgrade")
		}
		proof := newSource
		if _, err := os.Lstat(oldSource); err == nil {
			proof = oldSource
		}
		verified, err := namespaceFile(proof)
		if err != nil || string(verified.raw) != move.ID {
			return nil, errors.New("original move namespace is not bound to its source receipt")
		}
		if err := appendNamespaceMarker(&plan, oldSource, newSource, move.ID); err != nil {
			return nil, err
		}
		if err := appendNamespaceMarker(&plan, filepath.Join(move.Target, previousCompletionMarker), filepath.Join(move.Target, completionMarker), move.ID); err != nil {
			return nil, err
		}
		stageInfo, err := os.Lstat(oldStage)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || index != 0 || !stageInfo.IsDir() {
			return nil, errors.New("original migration staging does not belong to a pending move")
		}
		newStage := move.Target + ".x-science-migrate-" + move.ID
		if _, err := os.Lstat(newStage); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("both migration staging namespaces exist or cannot be inspected")
		}
		if _, err := emptyDirectoryOrMissing(move.Target); err != nil {
			return nil, err
		}
		if err := validateNamespaceStaging(oldStage, move.ID); err != nil {
			return nil, err
		}
		if err := appendNamespaceMarker(&plan, filepath.Join(oldStage, previousCompletionMarker), filepath.Join(oldStage, completionMarker), move.ID); err != nil {
			return nil, err
		}
		plan = append(plan, namespaceMoveOperation{source: oldStage, target: newStage, directory: stageInfo})
	}
	return plan, nil
}

func namespaceMoveID(id string) bool {
	if id == "" || len(id) > 128 || id == "." || id == ".." || strings.HasSuffix(id, ".") || strings.ContainsAny(id, `/\\<>:"|?*`) {
		return false
	}
	for _, character := range id {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func appendNamespaceMarker(plan *[]namespaceMoveOperation, source, target, id string) error {
	if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	snapshot, err := namespaceFile(source)
	if err != nil || string(snapshot.raw) != id {
		return errors.New("original namespace marker does not match its move receipt")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("both namespace markers exist or cannot be inspected")
	}
	*plan = append(*plan, namespaceMoveOperation{source: source, target: target, snapshot: snapshot})
	return nil
}

func applyMoveNamespace(plan []namespaceMoveOperation) error {
	for _, operation := range plan {
		if operation.directory == nil {
			if err := namespaceRename(operation.source, operation.target, operation.snapshot); err != nil {
				return err
			}
			continue
		}
		info, err := os.Lstat(operation.source)
		if err != nil || !os.SameFile(operation.directory, info) || !info.IsDir() {
			return errors.New("original staging directory changed during namespace upgrade")
		}
		if err := namespaceRenameExclusive(operation.source, operation.target); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Dir(operation.target)); err != nil {
			return err
		}
	}
	return nil
}

func validateNamespaceStaging(staging, id string) error {
	if _, err := canonicalExistingDirectory(staging); err != nil {
		return err
	}
	info, err := os.Lstat(staging)
	if err != nil {
		return err
	}
	if err := namespaceOwned(staging, info, true); err != nil {
		return err
	}
	marker := filepath.Join(staging, completionMarker)
	if _, err := os.Lstat(marker); errors.Is(err, os.ErrNotExist) {
		marker = filepath.Join(staging, previousCompletionMarker)
	}
	proof, err := namespaceFile(marker)
	if err != nil || string(proof.raw) != id {
		return errors.New("migration staging has no matching completion receipt; incomplete data is retained for recovery")
	}
	entries := 0
	return filepath.WalkDir(staging, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > 1_000_000 {
			return errors.New("migration staging exceeds the namespace verification inventory bound")
		}
		actual, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !actual.Mode().IsRegular()) {
			return errors.New("migration staging contains an aliased or non-regular entry")
		}
		return namespaceOwned(path, actual, false)
	})
}

// A complete receipt-bound staging tree can be committed after a restart.
// Partial copies remain untouched and fail closed; they are never reclassified
// as successful merely because the namespace changed.
func commitRecoveredNamespaceStaging(staging, target, id string, targetExists bool) error {
	if err := validateNamespaceStaging(staging, id); err != nil {
		return err
	}
	if targetExists {
		if _, err := emptyDirectoryOrMissing(target); err != nil {
			return err
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("remove verified empty recovery target: %w", err)
		}
	}
	if err := namespaceRenameExclusive(staging, target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}
