package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"x-science/internal/compute/namespace"
	"x-science/internal/compute/transfer"
)

// Exact old schema is a historical input fact, never a new output identity.
const previousNamespaceSchema = "synon.compute-checkpoint.v1"

func namespaceSchemaMatches(value string, historical bool) bool {
	return value == Schema || historical && value == previousNamespaceSchema
}

func namespaceSchemaError(value string, historical bool) error {
	if !historical && value == previousNamespaceSchema {
		return namespace.ErrRecoveryUnconfirmed
	}
	return nil
}

// ReadWithHistoricalProof verifies the complete original source digest before
// any file callback. A seekable immutable observation is required; untrusted
// streamed uploads do not gain old-schema acceptance through this entrypoint.
func ReadWithHistoricalProof(ctx context.Context, source io.ReadSeeker, expectedInput, expectedCommand, expectedManifest string, consume func(File) error, proof namespace.SourceProof) (Receipt, error) {
	if ctx == nil || source == nil || proof.VerifyRead(expectedInput, expectedManifest) != nil || !validSHA(expectedManifest) {
		return Receipt{}, namespace.ErrRecoveryUnconfirmed
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return Receipt{}, errors.Join(namespace.ErrRecoveryUnconfirmed, err)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, transfer.ReaderWithContext(ctx, source)); err != nil {
		return Receipt{}, errors.Join(namespace.ErrRecoveryUnconfirmed, err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != expectedManifest {
		return Receipt{}, namespace.ErrRecoveryUnconfirmed
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return Receipt{}, errors.Join(namespace.ErrRecoveryUnconfirmed, err)
	}
	receipt, err := read(ctx, source, expectedInput, expectedCommand, consume, true)
	if err != nil || receipt.ManifestSHA256 != expectedManifest {
		return Receipt{}, errors.Join(namespace.ErrRecoveryUnconfirmed, err)
	}
	return receipt, nil // Preserve the actual source schema and SHA; do not relabel bytes.
}
