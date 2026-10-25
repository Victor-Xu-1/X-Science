package webfetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
)

// TextContentDecoding describes only a projection of the received bytes. It
// does not change BytesRead or the original body digest/recovery authority.
type TextContentDecoding struct {
	Format       string `json:"format"`
	Layers       int    `json:"layers"`
	DecodedBytes int    `json:"decodedBytes"`
	Truncated    bool   `json:"truncated,omitempty"`
}

type webTextProjection struct {
	bytes     []byte
	layers    int
	truncated bool
}

const maxWebTextContentLayers = 2

func webResponseHasTextContract(contentType string) bool {
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	return strings.HasPrefix(media, "text/") || strings.Contains(media, "json") ||
		strings.Contains(media, "xml") || strings.Contains(media, "javascript") ||
		media == "application/x-www-form-urlencoded"
}

func webGzipContentPrefix(body []byte) bool {
	return len(body) >= 3 && body[0] == 0x1f && body[1] == 0x8b && body[2] == 0x08
}

func projectWebTextContent(ctx context.Context, body []byte, contentType string, incomplete bool, limit int64) (webTextProjection, error) {
	projection := webTextProjection{bytes: body}
	if !webResponseHasTextContract(contentType) {
		return projection, nil
	}
	for webGzipContentPrefix(projection.bytes) {
		if err := ctx.Err(); err != nil {
			return projection, err
		}
		if projection.layers == maxWebTextContentLayers {
			return projection, errors.New("text content encoding nesting exceeds the bounded decoder")
		}
		reader, err := gzip.NewReader(bytes.NewReader(projection.bytes))
		if err != nil {
			return projection, err
		}
		projection.layers++
		decoded, readErr := io.ReadAll(io.LimitReader(webTextContextReader{ctx: ctx, reader: reader}, limit+1))
		closeErr := reader.Close()
		if err := ctx.Err(); err != nil {
			return projection, err
		}
		if readErr != nil && (!(incomplete || projection.truncated) || !errors.Is(readErr, io.ErrUnexpectedEOF) || len(decoded) == 0) {
			return projection, readErr
		}
		if closeErr != nil && (!(incomplete || projection.truncated) || !errors.Is(closeErr, io.ErrUnexpectedEOF) || len(decoded) == 0) {
			return projection, closeErr
		}
		if int64(len(decoded)) > limit {
			decoded = decoded[:limit]
			projection.truncated = true
		}
		projection.bytes = decoded
	}
	return projection, nil
}

type webTextContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader webTextContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
