package productstate

import (
	"errors"
	"path/filepath"
)

// CanonicalTeamFilePath accepts only a typed existing team-record coordinate,
// never a arbitrary file request. Historical coordinates require the matching
// completed source receipt, and are never allowed to delete recovery sources.
func CanonicalTeamFilePath(root, relative, name string) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", errors.New("invalid team resource identity")
	}
	canonical := filepath.ToSlash(filepath.Join(HiddenDirectory, "teams", name+".json"))
	if relative == canonical {
		return canonical, nil
	}
	for _, directory := range previousHiddenDirectories {
		if relative != filepath.ToSlash(filepath.Join(directory, "teams", name+".json")) {
			continue
		}
		root, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		receipt, found, err := readReceipt(root, filepath.Join(root, HiddenDirectory, "namespace-upgrade.json"))
		if err != nil || !found || receipt.Sources["teams"].Directory != directory {
			return "", errors.New("historical team path has no namespace receipt")
		}
		if err := checkPath(root, filepath.Join(root, filepath.FromSlash(canonical))); err != nil {
			return "", err
		}
		return canonical, nil
	}
	return "", errors.New("team resource coordinate is unconfirmed")
}
