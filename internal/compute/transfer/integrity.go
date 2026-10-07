package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

func VerifyFile(ctx context.Context, location, expected string, bytes int64) error {
	info, err := os.Lstat(location)
	if err != nil || !info.Mode().IsRegular() || info.Size() != bytes || bytes < 0 {
		return errors.New("output file does not match its regular-file receipt")
	}
	source, err := os.Open(location)
	if err != nil {
		return err
	}
	defer source.Close()
	digest := sha256.New()
	if _, err := io.CopyBuffer(digest, contextReader{ctx, source}, make([]byte, 128<<10)); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected {
		return errors.New("output file digest changed")
	}
	return nil
}

type contextReader struct {
	context context.Context
	reader  io.Reader
}

func ReaderWithContext(ctx context.Context, reader io.Reader) io.Reader {
	return contextReader{ctx, reader}
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
