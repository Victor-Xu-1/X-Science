package server

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"x-science/internal/sciencecapability"
)

// These names identify immutable host receipts, not current product branding.
const publishedManagedExecutionOwnerMarker = ".synon-execution-pack.json"
const publishedManagedExecutionOwnerSchema = "synon.execution-pack-output-owner.v1"
const publishedManagedExecutionSnapshotMarker = ".synon-output-snapshot.json"
const publishedManagedExecutionSnapshotSchema = "synon.execution-pack-output-snapshot.v1"
const publishedManagedExecutionArtifactDirectory = ".synon-artifacts"

func isManagedExecutionOwnerMarker(name string) bool {
	return name == managedExecutionOutputOwnershipMarker || name == publishedManagedExecutionOwnerMarker
}

func managedExecutionReceiptMarkerPath(root string, writes map[string]string) (string, error) {
	selected := ""
	for _, name := range []string{managedExecutionOutputOwnershipMarker, publishedManagedExecutionOwnerMarker} {
		path := filepath.Join(root, name)
		if writes[path] == "" {
			continue
		}
		if selected != "" {
			return "", errors.New("managed execution receipt has ambiguous ownership markers")
		}
		selected = path
	}
	if selected == "" {
		return "", errors.New("managed execution output has no marker receipt")
	}
	return selected, nil
}

func readManagedExecutionMetadata(directory, name string) ([]byte, error) {
	return readManagedExecutionMetadataLimit(directory, name, 4096)
}

func readManagedExecutionMetadataLimit(directory, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	entry, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("managed execution metadata is not a regular file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("managed execution metadata size or type is invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("managed execution metadata read exceeds its bound")
	}
	return raw, nil
}

func readManagedExecutionSnapshotManifest(directory string) (managedExecutionSnapshotManifest, error) {
	var selected managedExecutionSnapshotManifest
	for _, format := range []struct{ name, schema string }{
		{".x-science-output-snapshot.json", managedExecutionSnapshotSchema},
		{publishedManagedExecutionSnapshotMarker, publishedManagedExecutionSnapshotSchema},
	} {
		raw, err := readManagedExecutionMetadataLimit(directory, format.name, 16<<20)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || selected.Schema != "" {
			return managedExecutionSnapshotManifest{}, errors.New("managed execution snapshot metadata is unsafe or ambiguous")
		}
		if json.Unmarshal(raw, &selected) != nil || selected.Schema != format.schema {
			return managedExecutionSnapshotManifest{}, errors.New("managed execution snapshot schema is invalid")
		}
	}
	if selected.Schema == "" {
		return selected, errors.New("managed execution snapshot manifest is absent")
	}
	return selected, nil
}

func managedExecutionReceiptCommandMatches(skill string, pack sciencecapability.ExecutionPack, source string) bool {
	return managedExecutionPackCommandMatches(skill, pack, source, true)
}

func publishedManagedExecutionSnapshotDirectory(workspaceRoot string, authority managedExecutionOutputAuthority) string {
	return filepath.Join(workspaceRoot, publishedManagedExecutionArtifactDirectory, ".managed", managedExecutionSnapshotID(authority))
}

func findManagedExecutionSnapshot(workspaceRoot string, authority managedExecutionOutputAuthority) (string, bool, error) {
	for _, candidate := range []string{managedExecutionSnapshotDirectory(workspaceRoot, authority), publishedManagedExecutionSnapshotDirectory(workspaceRoot, authority)} {
		valid, err := managedExecutionSnapshotManifestAt(candidate, authority.PackID, authority.ExecutionID)
		if err != nil {
			return "", false, err
		}
		if valid {
			return candidate, true, nil
		}
	}
	return managedExecutionSnapshotDirectory(workspaceRoot, authority), false, nil
}
