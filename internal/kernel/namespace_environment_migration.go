package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Exact historical input facts are confined to this migration boundary. New
// installations and all normal writes retain the current namespace contracts.
const previousNamespaceEnvironmentMarkerName = ".synon-managed-environment.json"

type managedEnvironmentNamespaceSource struct {
	SHA256     string `json:"sha256"`
	MarkerJSON string `json:"markerJSON"`
}

func previousNamespaceEnvironmentGeneration(marker managedEnvironmentMarker) string {
	contract := "synon-managed-environment-v3-final-prefix"
	if marker.ValidationRevision > 0 {
		contract = "synon-managed-environment-v4-verified-installation"
	}
	if marker.SchemaVersion == managedEnvironmentLegacyVersion {
		contract = "synon-managed-environment-v1"
		if marker.SpecDigest != "" {
			contract = "synon-managed-environment-v2"
		}
	}
	value := contract + "\x00" + marker.Name + "\x00" + marker.Language + "\x00"
	if marker.Kind == "path-venv" {
		value = "synon-managed-registered-environment-v1\x00" + marker.Name + "\x00" + marker.Language + "\x00" + marker.SourcePath + "\x00" + marker.RuntimePath + "\x00"
	} else if marker.SpecDigest != "" {
		value += marker.SpecDigest + "\x00"
	}
	for _, item := range marker.Packages {
		value += item + "\x00"
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func validatePreviousNamespaceEnvironmentMarker(marker managedEnvironmentMarker) (managedEnvironmentMarker, error) {
	proof := marker.PreviousNamespace
	if proof == nil || len(proof.MarkerJSON) == 0 || len(proof.MarkerJSON) > maxManagedEnvironmentInputBytes || !validSHA256(proof.SHA256) {
		return managedEnvironmentMarker{}, errors.New("previous namespace marker source is invalid")
	}
	digest := sha256.Sum256([]byte(proof.MarkerJSON))
	if hex.EncodeToString(digest[:]) != proof.SHA256 {
		return managedEnvironmentMarker{}, errors.New("previous namespace marker source digest does not match")
	}
	var source managedEnvironmentMarker
	decoder := json.NewDecoder(strings.NewReader(proof.MarkerJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&source) != nil || source.PreviousNamespace != nil {
		return managedEnvironmentMarker{}, errors.New("previous namespace marker source contract is invalid")
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return managedEnvironmentMarker{}, errors.New("previous namespace marker source contains trailing data")
	}
	if source.Kind == "" {
		source.Kind = "conda"
	}
	projected := marker
	projected.PreviousNamespace = nil
	if !reflect.DeepEqual(projected, source) || marker.Generation != previousNamespaceEnvironmentGeneration(source) {
		return managedEnvironmentMarker{}, errors.New("previous namespace marker does not match its immutable generation")
	}
	if marker.Kind == "path-venv" {
		sourcePath, sourceErr := canonicalManagedRegistrationPath(marker.SourcePath)
		runtimePath, runtimeErr := canonicalManagedRegistrationPath(marker.RuntimePath)
		if sourceErr != nil || runtimeErr != nil || sourcePath != marker.SourcePath || runtimePath != marker.RuntimePath {
			return managedEnvironmentMarker{}, errors.New("previous namespace registered environment paths changed")
		}
	} else if marker.Kind != "conda" {
		return managedEnvironmentMarker{}, errors.New("previous namespace environment kind is invalid")
	}
	return marker, nil
}

func readPreviousNamespaceEnvironmentMarker(prefix string) (managedEnvironmentMarker, error) {
	var marker managedEnvironmentMarker
	previousPath := filepath.Join(prefix, previousNamespaceEnvironmentMarkerName)
	if info, err := os.Lstat(previousPath); err != nil || !info.Mode().IsRegular() {
		return managedEnvironmentMarker{}, errors.New("previous namespace marker is not an owned regular file")
	}
	raw, err := readBoundedJSON(previousPath, &marker)
	if err != nil || marker.PreviousNamespace != nil {
		return managedEnvironmentMarker{}, errors.New("previous namespace environment marker is unavailable")
	}
	digest := sha256.Sum256(raw)
	marker.PreviousNamespace = &managedEnvironmentNamespaceSource{SHA256: hex.EncodeToString(digest[:]), MarkerJSON: string(raw)}
	return validateManagedEnvironmentMarker(marker)
}

// UpgradeManagedEnvironmentNamespace migrates only verified generation marker
// metadata. It never moves a Conda prefix, changes a generation/operation ID,
// starts an installer or rewrites a registration's external paths. The original
// marker bytes remain hash-bound recovery provenance in the canonical marker.
func (m *Manager) UpgradeManagedEnvironmentNamespace(ctx context.Context) error {
	if m == nil || ctx == nil {
		return errors.New("managed environment namespace upgrade requires a manager and context")
	}
	root, err := m.managedEnvironmentRoot()
	if err != nil {
		return err
	}
	generations := filepath.Join(root, ".generations")
	resolved, resolveErr := filepath.EvalSymlinks(generations)
	if resolveErr == nil && resolved != generations {
		return errors.New("managed environment generation namespace root is aliased")
	}
	names, err := os.ReadDir(generations)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(names) > 1024 {
		return errors.New("managed environment namespace inventory is unavailable or oversized")
	}
	for _, name := range names {
		if !name.IsDir() || !ValidEnvironmentName(name.Name()) || strings.HasPrefix(name.Name(), ".") {
			continue
		}
		generationRoot := filepath.Join(root, ".generations", name.Name())
		release, err := lockKernelFile(ctx, filepath.Join(generationRoot, ".install.lock"))
		if err != nil {
			return err
		}
		err = upgradeManagedEnvironmentNamespaceDirectory(ctx, generationRoot, name.Name())
		release()
		if err != nil {
			return err
		}
	}
	return nil
}

func upgradeManagedEnvironmentNamespaceDirectory(ctx context.Context, root, name string) error {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) > maxManagedEnvironmentGenerations {
		return errors.New("managed environment generation namespace inventory is unavailable or oversized")
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || !validSHA256(entry.Name()) {
			continue
		}
		prefix := filepath.Join(root, entry.Name())
		previous := filepath.Join(prefix, previousNamespaceEnvironmentMarkerName)
		if _, err := os.Lstat(previous); errors.Is(err, os.ErrNotExist) {
			continue
		}
		marker, err := readManagedEnvironmentMarker(prefix)
		if err != nil || marker.PreviousNamespace == nil || marker.Name != name || marker.Generation != entry.Name() {
			return errors.New("managed environment previous namespace generation cannot be verified")
		}
		current := filepath.Join(prefix, managedEnvironmentMarkerName)
		if _, err := os.Lstat(current); errors.Is(err, os.ErrNotExist) {
			if err := writeManagedEnvironmentMarker(current, marker); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		verified, err := readManagedEnvironmentMarker(prefix)
		if err != nil || !reflect.DeepEqual(verified, marker) {
			return errors.New("managed environment namespace publication cannot be verified")
		}
		var original managedEnvironmentMarker
		raw, err := readBoundedJSON(previous, &original)
		digest := sha256.Sum256(raw)
		if err != nil || hex.EncodeToString(digest[:]) != marker.PreviousNamespace.SHA256 {
			return errors.New("managed environment previous namespace source changed")
		}
		if err := os.Remove(previous); err != nil {
			return err
		}
		if err := syncDirectory(prefix); err != nil {
			return err
		}
	}
	return nil
}
