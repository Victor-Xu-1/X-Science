package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
)

// resolveNamespaceImportedEnvironmentInput is a reader-only alias for the
// actual registered resource. It cannot create an old live registry name,
// reinterpret a user-selected substring or mint import provenance.
func (m *Manager) resolveNamespaceImportedEnvironmentInput(name string) (string, error) {
	current := namespaceRegisteredCurrentName(name)
	if current == name || name == "" {
		return name, nil
	}
	if err := validateNamespaceRegistryTargetRoot(m.config.CondaEnvsPath); err != nil {
		return "", err
	}
	root, err := m.managedEnvironmentRoot()
	if err != nil {
		return "", err
	}
	prefix, err := filepath.EvalSymlinks(filepath.Join(root, current))
	if err != nil || filepath.Dir(prefix) != filepath.Join(root, ".generations", current) {
		return "", errors.New("historical environment input requires its verified canonical registration")
	}
	marker, err := readManagedEnvironmentMarker(prefix)
	if err != nil || marker.Name != current || marker.Generation != filepath.Base(prefix) || marker.NamespaceImport == nil || marker.NamespaceImport.SourceName != name {
		return "", errors.New("historical environment input does not match the actual imported source")
	}
	return current, nil
}

// Bind only exact resource-name fields from an actual imported source. The
// caller's typed request ordering and all package/path/user values are retained
// so the original operation key cannot authorize a changed installation.
func (m *Manager) previousNamespaceImportedRequestContext(ctx context.Context, operationID, kind string, definition any, currentName string) context.Context {
	marker, err := m.activeManagedEnvironmentMarker(currentName)
	if err != nil || marker.NamespaceImport == nil || marker.NamespaceImport.SourceName == currentName {
		return ctx
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		return ctx
	}
	current, _ := json.Marshal(currentName)
	original, _ := json.Marshal(marker.NamespaceImport.SourceName)
	changed := raw
	for _, field := range []string{"Name", "TargetName"} {
		from := append([]byte("\""+field+"\":"), current...)
		to := append([]byte("\""+field+"\":"), original...)
		changed = bytes.Replace(changed, from, to, 1)
	}
	if bytes.Equal(changed, raw) {
		return ctx
	}
	previousContext := previousNamespaceRequestContext(ctx, operationID, kind, json.RawMessage(changed))
	currentKeys, _ := ctx.Value(previousNamespaceOperationKeysContext{}).([]string)
	previousKeys, _ := previousContext.Value(previousNamespaceOperationKeysContext{}).([]string)
	keys := append(append([]string(nil), currentKeys...), previousKeys...)
	return context.WithValue(ctx, previousNamespaceOperationKeysContext{}, keys)
}
