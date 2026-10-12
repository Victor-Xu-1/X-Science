package datadir

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	productidentity "x-science"
)

// UpgradeDefaultPointer transfers only the historical, product-owned default
// pointer. Custom operator paths deliberately bypass this function. Original
// JSON bytes are retained, including future fields, move IDs and activations.
func UpgradeDefaultPointer(previous, current string) error {
	if !filepath.IsAbs(previous) || !filepath.IsAbs(current) ||
		filepath.Base(previous) != "data-dir.json" || filepath.Base(current) != "data-dir.json" ||
		filepath.Base(filepath.Dir(previous)) != "synon-go" || // Original default path: upgrade input, not current branding.
		filepath.Base(filepath.Dir(current)) != productidentity.Current().MachineSlug ||
		filepath.Dir(filepath.Dir(previous)) != filepath.Dir(filepath.Dir(current)) {
		return errors.New("namespace upgrade requires the exact sibling default pointer paths")
	}
	if _, err := os.Lstat(previous); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	parent := filepath.Dir(current)
	if _, err := canonicalExistingDirectory(filepath.Dir(parent)); err != nil {
		return err
	}
	if err := os.Mkdir(parent, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() {
		return errors.New("current default pointer parent is not a real directory")
	} else if err := namespaceOwned(parent, info, true); err != nil {
		return err
	}
	if _, err := canonicalExistingDirectory(parent); err != nil {
		return err
	}
	lock := filepath.Join(parent, ".namespace-pointer-upgrade.lock")
	guard, err := lockNamespacePointer(lock)
	if err != nil {
		return fmt.Errorf("default pointer upgrade is already active or its lock is unsafe: %w", err)
	}
	defer releaseNamespaceLock(guard)
	// Another process may have completed the transfer while this invocation
	// prepared its directories. Only facts re-read under the OS lock authorize
	// publication; abandoned text in the lock file is not a live owner.
	if _, err := os.Lstat(previous); errors.Is(err, os.ErrNotExist) {
		published, err := namespaceFile(current)
		if err != nil {
			return errors.New("original default pointer disappeared without a verified canonical pointer")
		}
		var publishedState State
		if err := json.Unmarshal(published.raw, &publishedState); err != nil || publishedState.SchemaVersion != stateSchemaVersion {
			return errors.New("canonical pointer published by another upgrader has an unsupported schema")
		}
		return nil
	} else if err != nil {
		return err
	}
	snapshot, err := namespaceFile(previous)
	if err != nil {
		return err
	}
	var state State
	if err := json.Unmarshal(snapshot.raw, &state); err != nil || state.SchemaVersion != stateSchemaVersion {
		return errors.New("original default pointer changed to an unsupported schema before lock acquisition")
	}
	if _, err := os.Lstat(current); err == nil {
		return errors.New("both default data pointers exist after lock acquisition; neither will be overwritten")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	plan, err := planMoveNamespace(state)
	if err != nil {
		return err
	}
	// The old pointer remains active until the one atomic, no-replace rename.
	// A crash leaves either the original or canonical pointer, never a second
	// writable pointer. The immutable hash-named copy is recovery, not authority.
	recovery := filepath.Join(parent, fmt.Sprintf(".namespace-pointer-source-%x.json", snapshot.hash))
	if err := namespaceRecovery(recovery, snapshot); err != nil {
		return err
	}
	if err := snapshot.unchanged(previous); err != nil {
		return err
	}
	if err := applyMoveNamespace(plan); err != nil {
		return err
	}
	return namespaceRename(previous, current, snapshot)
}
