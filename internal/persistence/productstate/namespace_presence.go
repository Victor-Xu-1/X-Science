package productstate

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func namespaceRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("private product-state root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil || filepath.Dir(root) == root {
		return "", errors.New("invalid private product-state root")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return "", errors.New("private product-state root is missing or aliased")
	}
	if err := requireDirectory(root, root); err != nil {
		return "", err
	}
	return root, nil
}

// EnsureHiddenNamespace is the normal-operation fence, not a migration replay.
// A completed durable receipt is already the verified source fact. Ordinary
// OAuth operations check only its typed shape and actual canonical coordinates;
// they never hash unrelated speech/models/teams or republish missing children.
func EnsureHiddenNamespace(ctx context.Context, root string) error {
	if ctx == nil {
		return errors.New("private product-state context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := namespaceRoot(root)
	if err != nil {
		return err
	}
	canonical := filepath.Join(root, HiddenDirectory)
	if err := checkPath(root, canonical); err != nil {
		return err
	}
	receipt, found, err := readReceipt(root, filepath.Join(canonical, "namespace-upgrade.json"))
	if err != nil {
		return err
	}
	if found {
		return validateCompletedPresence(root, receipt)
	}
	if _, err := os.Lstat(filepath.Join(canonical, "namespace-upgrade.pending.json")); err == nil {
		return UpgradeHiddenNamespace(ctx, root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// The first direct-library use still performs the genuine full migration.
	// With no historical owned sources, fresh current stores need no upgrade.
	for _, directory := range previousHiddenDirectories {
		if err := checkPath(root, filepath.Join(root, directory)); err != nil {
			return err
		}
		for _, child := range ownedChildren {
			if _, err := os.Lstat(filepath.Join(root, directory, child)); err == nil {
				return UpgradeHiddenNamespace(ctx, root)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	for _, child := range []string{"mcp-oauth-vault", "mcp-oauth"} {
		if err := checkPath(root, filepath.Join(canonical, child)); err != nil {
			return err
		}
	}
	return nil
}

func validateCompletedPresence(root string, receipt namespaceReceipt) error {
	for child, source := range receipt.Sources {
		digest, err := hex.DecodeString(source.SHA256)
		if !slices.Contains(ownedChildren, child) || !slices.Contains(previousHiddenDirectories, source.Directory) || err != nil || len(digest) != 32 || strings.ToLower(source.SHA256) != source.SHA256 {
			return errors.New("private namespace receipt source is invalid")
		}
		if err := requireDirectory(root, filepath.Join(root, HiddenDirectory, child)); err != nil {
			return err
		}
		if child != "mcp-oauth-vault" {
			continue
		}
		// The original recovery vault determines which published key/cipher
		// coordinates existed. A deleted current key/cipher is not a new empty
		// store and cannot trigger source recopy or credential reinitialization.
		for _, filename := range []string{"master.key", "vault.enc"} {
			original := filepath.Join(root, source.Directory, child, "secrets", filename)
			info, err := os.Lstat(original)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if err := checkPath(root, original); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("private namespace recovery vault coordinate is unconfirmed")
			}
			target := filepath.Join(root, HiddenDirectory, child, "secrets", filename)
			if err := checkPath(root, target); err != nil {
				return err
			}
			info, err = os.Lstat(target)
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("current private vault evidence is missing; recovery required")
			}
		}
	}
	return nil
}
