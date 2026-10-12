// Package productstate owns the fixed private product-state directory contract.
package productstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

const HiddenDirectory = ".x-science"

// These are genuine historical directory contracts, not current write roots.
var previousHiddenDirectories = []string{".synon", ".xscience"}
var ownedChildren = []string{"mcp-oauth-vault", "mcp-oauth", "teams", "local-speech"}
var namespaceLock = make(chan struct{}, 1)

type sourceReceipt struct {
	Directory string `json:"directory"`
	SHA256    string `json:"sha256"`
}
type namespaceReceipt struct {
	Version int                      `json:"version"`
	Sources map[string]sourceReceipt `json:"sources"`
}

// UpgradeHiddenNamespace runs before private stores or background writers open.
// Only the fixed owned children copy into the current authority. Original bytes
// remain as recovery evidence; a durable source receipt prevents a later copy
// from replacing current writes. Interrupted copies resume only exact receipts.
func UpgradeHiddenNamespace(ctx context.Context, root string) error {
	if ctx == nil {
		return errors.New("private product-state context and root are required")
	}
	select {
	case namespaceLock <- struct{}{}:
		defer func() { <-namespaceLock }()
	case <-ctx.Done():
		return ctx.Err()
	}
	root, err := namespaceRoot(root)
	if err != nil {
		return err
	}
	// Preflight every owned source and target before creating a lock or target.
	if _, err := inspectSources(ctx, root); err != nil {
		return err
	}
	if err := checkPath(root, filepath.Join(root, HiddenDirectory)); err != nil {
		return err
	}
	for _, child := range ownedChildren {
		target := filepath.Join(root, HiddenDirectory, child)
		if _, err := os.Lstat(target); err == nil {
			if _, err := treeSHA256(ctx, root, target); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	lock, err := lockNamespace(ctx, filepath.Join(root, ".x-science.namespace-upgrade.lock"))
	if err != nil {
		return err
	}
	defer unlockNamespace(lock)
	sources, err := inspectSources(ctx, root)
	if err != nil {
		return err
	}
	canonical := filepath.Join(root, HiddenDirectory)
	completed := filepath.Join(canonical, "namespace-upgrade.json")
	pending := filepath.Join(canonical, "namespace-upgrade.pending.json")
	if receipt, found, err := readReceipt(root, completed); err != nil {
		return err
	} else if found {
		if !reflect.DeepEqual(receipt.Sources, sources) {
			return errors.New("private namespace recovery source changed")
		}
		return validateCompletedPresence(root, receipt)
	}
	if len(sources) == 0 {
		if _, found, err := readReceipt(root, pending); err != nil {
			return err
		} else if found {
			return errors.New("private namespace pending recovery source disappeared")
		}
		return nil
	}
	proof := namespaceReceipt{Version: 1, Sources: sources}
	if receipt, found, err := readReceipt(root, pending); err != nil {
		return err
	} else if found {
		if !reflect.DeepEqual(receipt, proof) {
			return errors.New("private namespace pending source changed")
		}
	} else {
		// A pre-existing owned target is foreign without a migration receipt.
		for child := range sources {
			if _, err := os.Lstat(filepath.Join(canonical, child)); !errors.Is(err, os.ErrNotExist) {
				return errors.New("private namespace target collision")
			}
		}
		if err := os.MkdirAll(canonical, 0o700); err != nil {
			return err
		}
		if err := checkPath(root, canonical); err != nil {
			return err
		}
		if err := publishPendingReceipt(root, pending, proof, writePrivateFile); err != nil {
			return err
		}
	}
	for _, child := range ownedChildren {
		source, found := sources[child]
		if !found {
			continue
		}
		target := filepath.Join(canonical, child)
		if _, err := os.Lstat(target); err == nil {
			actual, err := treeSHA256(ctx, root, target)
			if err != nil || actual != source.SHA256 {
				return errors.New("private namespace partial target is unconfirmed")
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		stage, err := os.MkdirTemp(canonical, ".namespace-copy-")
		if err != nil {
			return err
		}
		// A failed stage stays private recovery evidence. Source/foreign paths
		// are never removed, and cannot become a second active write authority.
		if err := copyOwnedTree(ctx, root, filepath.Join(root, source.Directory, child), stage); err != nil {
			return err
		}
		actual, err := treeSHA256(ctx, root, stage)
		if err != nil || actual != source.SHA256 {
			return errors.New("private namespace copied evidence differs")
		}
		current, err := treeSHA256(ctx, root, filepath.Join(root, source.Directory, child))
		if err != nil || current != source.SHA256 {
			return errors.New("private namespace source changed during copy")
		}
		if err := publishMissing(stage, target); err != nil {
			return err
		}
		if err := syncDirectory(canonical); err != nil {
			return err
		}
	}
	if err := publishMissing(pending, completed); err != nil {
		return err
	}
	if err := syncDirectory(canonical); err != nil {
		return err
	}
	// Read back the durable authority before permitting any vault initialization.
	receipt, found, err := readReceipt(root, completed)
	if err != nil || !found || !reflect.DeepEqual(receipt, proof) {
		return errors.New("private namespace receipt readback failed")
	}
	return nil
}

func inspectSources(ctx context.Context, root string) (map[string]sourceReceipt, error) {
	result := map[string]sourceReceipt{}
	for _, directory := range previousHiddenDirectories {
		if err := checkPath(root, filepath.Join(root, directory)); err != nil {
			return nil, err
		}
		for _, child := range ownedChildren {
			path := filepath.Join(root, directory, child)
			if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return nil, err
			}
			if _, duplicate := result[child]; duplicate {
				return nil, errors.New("competing private namespace sources")
			}
			if err := requireDirectory(root, path); err != nil {
				return nil, err
			}
			digest, err := treeSHA256(ctx, root, path)
			if err != nil {
				return nil, err
			}
			result[child] = sourceReceipt{Directory: directory, SHA256: digest}
		}
	}
	return result, nil
}

func readReceipt(root, path string) (namespaceReceipt, bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return namespaceReceipt{}, false, nil
	}
	if err := checkPath(root, path); err != nil {
		return namespaceReceipt{}, false, err
	}
	raw, err := readOwnedFile(path)
	if err != nil {
		return namespaceReceipt{}, false, err
	}
	var receipt namespaceReceipt
	if len(raw) > 65536 || json.Unmarshal(raw, &receipt) != nil || receipt.Version != 1 || len(receipt.Sources) == 0 || len(receipt.Sources) > len(ownedChildren) {
		return receipt, false, errors.New("invalid private namespace receipt")
	}
	return receipt, true, nil
}
