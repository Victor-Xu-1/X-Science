package transfer

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"x-science/internal/compute/namespace"
)

const previousNamespaceManifestSchema = "synon.compute-output-manifest.v1"

func namespaceManifestSchemaMatches(value string, historical bool) bool {
	return value == ManifestSchema || historical && value == previousNamespaceManifestSchema
}

func namespaceManifestSchemaError(value string, historical bool) error {
	if !historical && value == previousNamespaceManifestSchema {
		return namespace.ErrRecoveryUnconfirmed
	}
	return nil
}

// Historical extraction preserves original manifest bytes and follows the same
// payload/path/capacity authority as current extraction. It never rewrites an
// uploaded schema string or grants permission to execute a provider workload.
func ExtractSelectedWithHistoricalProof(ctx context.Context, archivePath, manifestPath, target string, selection Selection, inputSHA256 string, proof namespace.SourceProof) (Extraction, error) {
	digest, err := hex.DecodeString(selection.ManifestSHA256)
	if ctx == nil || proof.VerifyRead(inputSHA256, selection.ManifestSHA256) != nil || err != nil || len(digest) != 32 || len(selection.ManifestSHA256) != 64 || selection.ManifestSHA256 != strings.ToLower(selection.ManifestSHA256) {
		return Extraction{}, namespace.ErrRecoveryUnconfirmed
	}
	result, err := extractSelected(ctx, archivePath, manifestPath, target, selection, true)
	if err != nil {
		return result, errors.Join(namespace.ErrRecoveryUnconfirmed, err)
	}
	return result, nil
}
