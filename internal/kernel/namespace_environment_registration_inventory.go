package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// NamespaceEnvironmentImport is an administrator-only frozen observation, not
// an execution permit. Its private source cannot be minted by uploaded JSON.
type NamespaceEnvironmentImport struct {
	CurrentName, SourceName, Generation, SourcePrefix, SourceSHA256, NameOrigin string
	Packages                                                                    []string
	source                                                                      managedEnvironmentNamespaceRegistrationSource
}

func InspectManagedEnvironmentNamespaceImports(ctx context.Context, previousRoot string) ([]NamespaceEnvironmentImport, error) {
	if ctx == nil {
		return nil, errors.New("environment registration requires a context")
	}
	root, err := canonicalManagedRegistrationPath(previousRoot)
	if err != nil || root != filepath.Clean(previousRoot) {
		return nil, errors.New("previous environment root is unavailable or aliased")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) > 4096 {
		return nil, errors.New("previous environment inventory is unavailable or oversized")
	}
	result := []NamespaceEnvironmentImport{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || namespaceSourceCoreName(entry.Name()) {
			continue
		}
		if !ValidEnvironmentName(entry.Name()) || entry.Type()&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("previous environment %q is not a managed activation", entry.Name())
		}
		observed, err := inspectNamespaceEnvironmentSource(ctx, root, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("inspect previous environment %q: %w", entry.Name(), err)
		}
		result = append(result, observed)
	}
	return result, nil
}

func inspectNamespaceEnvironmentSource(ctx context.Context, root, name string) (NamespaceEnvironmentImport, error) {
	if err := ctx.Err(); err != nil {
		return NamespaceEnvironmentImport{}, err
	}
	active := filepath.Join(root, name)
	target, err := os.Readlink(active)
	if err != nil {
		return NamespaceEnvironmentImport{}, err
	}
	prefix, err := filepath.EvalSymlinks(active)
	if err != nil {
		return NamespaceEnvironmentImport{}, err
	}
	generation := filepath.Base(prefix)
	if !validSHA256(generation) || prefix != filepath.Join(root, ".generations", name, generation) {
		return NamespaceEnvironmentImport{}, errors.New("source activation escapes its own physical generation")
	}
	var marker managedEnvironmentMarker
	raw, err := readBoundedJSON(filepath.Join(prefix, previousNamespaceEnvironmentMarkerName), &marker)
	if err != nil || marker.NamespaceImport != nil || marker.PreviousNamespace != nil {
		return NamespaceEnvironmentImport{}, errors.New("original environment marker is unavailable or nested")
	}
	digest := sha256.Sum256(raw)
	source := managedEnvironmentNamespaceRegistrationSource{SourceRoot: root, SourceName: name, SourcePrefix: prefix,
		SourceGeneration: generation, PointerTarget: target, MarkerJSON: string(raw), MarkerSHA256: hex.EncodeToString(digest[:]), NameOrigin: namespaceRegisteredNameOrigin(name)}
	// The source decoder verifies the published generation formula and every
	// typed recipe field before package/payload inventory is trusted.
	verified, err := decodeNamespaceRegistrationOriginalMarker(source)
	if err != nil {
		return NamespaceEnvironmentImport{}, err
	}
	source.InventorySHA256, err = namespaceCondaInventoryDigest(ctx, prefix, verified.Packages)
	if err != nil {
		return NamespaceEnvironmentImport{}, err
	}
	source.PayloadSHA256, source.UnresolvedDependencies, err = namespacePrefixPayloadDigest(ctx, prefix)
	if err != nil {
		return NamespaceEnvironmentImport{}, err
	}
	source.SourceSHA256 = namespaceRegistrationSourceDigest(source)
	return NamespaceEnvironmentImport{CurrentName: namespaceRegisteredCurrentName(name), SourceName: name,
		Generation: generation, SourcePrefix: prefix, SourceSHA256: source.SourceSHA256, NameOrigin: source.NameOrigin,
		Packages: append([]string(nil), verified.Packages...), source: source}, nil
}

func namespaceCondaInventoryDigest(ctx context.Context, prefix string, expected []string) (string, error) {
	root, err := os.OpenRoot(prefix)
	if err != nil {
		return "", err
	}
	defer root.Close()
	entries, err := readManagedInventoryDirectory(root, "conda-meta", 4096)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	digest := sha256.New()
	_, _ = io.WriteString(digest, "x-science.environment-installed-package-inventory.v1\x00")
	actual := map[string]bool{}
	installedNames := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		file, err := root.Open(filepath.Join("conda-meta", entry.Name()))
		if err != nil {
			return "", err
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			_ = file.Close()
			return "", errors.New("installed package record is malformed or oversized")
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, (8<<20)+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return "", errors.Join(readErr, closeErr)
		}
		var record struct{ Name, Version, Build string }
		if json.Unmarshal(raw, &record) != nil || !managedPackageName.MatchString(record.Name) || record.Version == "" || record.Build == "" {
			return "", errors.New("installed package record identity is invalid")
		}
		if installedNames[record.Name] || strings.ContainsAny(record.Version+record.Build, "\x00\r\n=") {
			return "", errors.New("installed package record identity is duplicate or unsafe")
		}
		installedNames[record.Name] = true
		actual[record.Name+"="+record.Version+"="+record.Build] = true
		chunk := sha256.Sum256(raw)
		_, _ = io.WriteString(digest, entry.Name()+"\x00"+hex.EncodeToString(chunk[:])+"\x00")
	}
	if len(actual) == 0 {
		return "", errors.New("installed Conda package inventory is empty")
	}
	expectedConda := map[string]bool{}
	for _, item := range expected {
		parts := strings.SplitN(item, "=", 4)
		if len(parts) < 4 {
			return "", errors.New("original package inventory has no resolved identity")
		}
		if parts[3] == "pypi" || parts[3] == "pip" || parts[2] == "pip" {
			continue // Pip files are captured by the physical payload witness.
		}
		if !actual[strings.Join(parts[:3], "=")] {
			return "", errors.New("installed package identity differs from the original marker")
		}
		expectedConda[strings.Join(parts[:3], "=")] = true
	}
	if len(actual) != len(expectedConda) {
		return "", errors.New("installed package inventory differs from the original marker")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
