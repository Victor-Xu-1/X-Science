package kernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type previousNamespaceOperationKeysContext struct{}

// Only normalized first-party environment fields change. User request strings,
// package sources, credentials, operation IDs and arbitrary JSON are untouched.
func previousNamespaceCreateContext(ctx context.Context, definition any) context.Context {
	return previousNamespaceRequestContext(ctx, "", "create", definition)
}

func previousNamespaceRequestContext(ctx context.Context, operationID, kind string, definition any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	operationID = strings.TrimSpace(operationID)
	if operationID != "" && kind != "create" && kind != "register" {
		definition = struct {
			OperationID string
			Input       any
		}{operationID, definition}
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		return ctx
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ctx
	}
	variants := [][]byte{raw}
	var source string
	if kind == "create" && json.Unmarshal(fields["SourceEnvironment"], &source) == nil && source == defaultManagedPythonEnvironment {
		// The normalized typed definition's field order is part of the old key.
		// Replace this one complete JSON field, not user values elsewhere.
		current, _ := json.Marshal(source)
		for _, previous := range []string{"synon-biomed-python", "synon-python"} {
			old, _ := json.Marshal(previous)
			variants = append(variants, replaceNamespaceSourceEnvironment(raw, current, old))
		}
	}
	keys := make([]string, 0, len(variants))
	for _, variant := range variants {
		digest := sha256.Sum256(append([]byte("synon-managed-environment-operation-v1\x00"+kind+"\x00"), variant...))
		keys = append(keys, hex.EncodeToString(digest[:]))
	}
	return context.WithValue(ctx, previousNamespaceOperationKeysContext{}, keys)
}

func replaceNamespaceSourceEnvironment(raw, current, previous []byte) []byte {
	// Marshal emits each field once. Matching its name as well as its quoted
	// value prevents changing a package name, path or prompt with the same text.
	from := append([]byte("\"SourceEnvironment\":"), current...)
	to := append([]byte("\"SourceEnvironment\":"), previous...)
	return bytes.Replace(raw, from, to, 1)
}

func namespaceOperationKeyMatches(ctx context.Context, marker managedEnvironmentMarker, current string) bool {
	if marker.OperationKey == current {
		return true
	}
	if marker.PreviousNamespace == nil {
		return false
	}
	previous, _ := ctx.Value(previousNamespaceOperationKeysContext{}).([]string)
	for _, key := range previous {
		if marker.OperationKey == key {
			return true
		}
	}
	return false
}
