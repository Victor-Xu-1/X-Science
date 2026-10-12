package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// An absent external target is an unresolved dependency fact, never a runtime
// authority. It is not followed, traversed or mounted by registration. Real
// Python/import/ABI readiness remains the existing manager's separate probe.
type namespaceEnvironmentUnresolvedDependency struct {
	RelativePath   string `json:"path"`
	LiteralTarget  string `json:"literalTarget"`
	ObservedAbsent bool   `json:"observedAbsent"`
	SHA256         string `json:"sha256"`
}

func namespaceUnresolvedDependencyDigest(item namespaceEnvironmentUnresolvedDependency) string {
	item.SHA256 = ""
	raw, _ := json.Marshal(item)
	digest := sha256.Sum256(append([]byte("x-science.environment-unresolved-dependency.v1\x00"), raw...))
	return hex.EncodeToString(digest[:])
}

func observeNamespaceUnresolvedDependency(prefix, relative, target string, resolveErr error) (namespaceEnvironmentUnresolvedDependency, error) {
	if !errors.Is(resolveErr, os.ErrNotExist) || !filepath.IsAbs(target) || filepath.Clean(target) != target ||
		hostPathContains(prefix, target) || strings.ContainsAny(target, "\x00\r\n") {
		return namespaceEnvironmentUnresolvedDependency{}, errors.New("physical environment link is malformed, cyclic or escapes its source")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return namespaceEnvironmentUnresolvedDependency{}, errors.New("physical environment external target is not absent")
	}
	item := namespaceEnvironmentUnresolvedDependency{RelativePath: relative, LiteralTarget: target, ObservedAbsent: true}
	item.SHA256 = namespaceUnresolvedDependencyDigest(item)
	return item, nil
}

func validateNamespaceUnresolvedDependencies(source managedEnvironmentNamespaceRegistrationSource) error {
	if len(source.UnresolvedDependencies) > 4096 {
		return errors.New("environment unresolved dependency inventory is oversized")
	}
	seen := map[string]bool{}
	for _, item := range source.UnresolvedDependencies {
		if !item.ObservedAbsent || item.SHA256 != namespaceUnresolvedDependencyDigest(item) ||
			filepath.IsAbs(item.RelativePath) || filepath.Clean(item.RelativePath) != item.RelativePath || item.RelativePath == "." ||
			item.RelativePath == ".." || strings.HasPrefix(item.RelativePath, ".."+string(os.PathSeparator)) || seen[item.RelativePath] {
			return errors.New("environment unresolved dependency receipt is invalid")
		}
		seen[item.RelativePath] = true
		path := filepath.Join(source.SourcePrefix, item.RelativePath)
		target, err := os.Readlink(path)
		if err != nil || target != item.LiteralTarget {
			return errors.New("environment unresolved dependency source link changed")
		}
		_, resolveErr := filepath.EvalSymlinks(path)
		observed, err := observeNamespaceUnresolvedDependency(source.SourcePrefix, item.RelativePath, target, resolveErr)
		if err != nil || observed != item {
			return errors.New("environment unresolved dependency target changed; no runtime authority granted")
		}
	}
	return nil
}
