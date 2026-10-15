package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"

	"x-science/internal/persistence/workspace"
)

type scientificArtifactByteStream struct {
	reader io.Reader
	digest hash.Hash
	bytes  int64
	err    error
}

func (stream *scientificArtifactByteStream) Read(data []byte) (int, error) {
	n, err := stream.reader.Read(data)
	if n > 0 {
		_, _ = stream.digest.Write(data[:n])
		stream.bytes += int64(n)
	}
	if err != nil && !errors.Is(err, io.EOF) && stream.err == nil {
		stream.err = err
	}
	return n, err
}

// The same cancellable stream supplies both the parser and the immutable-byte
// digest. An absent/early-exiting parser cannot skip the unread tail or turn an
// input error into an optional-capability warning. No second blob is created.
func validateScientificArtifactBytes(ctx context.Context, reader io.Reader, version workspace.ArtifactVersion, format string, validate scientificArtifactValidator) (scientificArtifactValidation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return scientificArtifactValidation{}, err
	}
	if reader == nil || validate == nil || version.SizeBytes < 0 || !isSHA256(version.ContentSHA256) {
		return scientificArtifactValidation{}, errors.New("scientific artifact content identity is invalid")
	}
	stream := &scientificArtifactByteStream{reader: &contextReader{ctx: ctx, reader: reader}, digest: sha256.New()}
	result, validationErr := validate(ctx, stream, format)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	_, readErr := io.Copy(io.Discard, stream)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if stream.err != nil || readErr != nil {
		return result, fmt.Errorf("read scientific artifact content: %w", errors.Join(stream.err, readErr))
	}
	if stream.bytes != version.SizeBytes {
		return result, errors.New("scientific artifact content size mismatch")
	}
	if !strings.EqualFold(hex.EncodeToString(stream.digest.Sum(nil)), version.ContentSHA256) {
		return result, errors.New("scientific artifact content digest mismatch")
	}
	return result, validationErr
}
