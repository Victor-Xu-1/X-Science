package webfetch

import (
	"context"
	"encoding/base64"
	"fmt"
	"unicode/utf8"

	"x-science/internal/httptext"
)

func assignWebResponseText(ctx context.Context, result *Result, body []byte, incomplete bool, limit int64) error {
	if result.Binary {
		return nil
	}
	compressed := webResponseHasTextContract(result.ContentType) && webGzipContentPrefix(body)
	projection, err := projectWebTextContent(ctx, body, result.ContentType, incomplete, limit)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if compressed {
		result.RawBodyBase64 = base64.StdEncoding.EncodeToString(body)
		result.TextDecoding = &TextContentDecoding{Format: "gzip", Layers: projection.layers}
	}
	if err != nil {
		webResponseTextFailure(result, body, "Public source content compression could not be decoded.")
		return nil
	}
	text, err := httptext.Decode(projection.bytes, result.ContentType, incomplete || projection.truncated)
	if err != nil {
		webResponseTextFailure(result, body, "Public source character encoding could not be decoded.")
		return nil
	}
	if compressed && int64(len(text)) > limit {
		text = text[:limit]
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		projection.truncated = true
	}
	if result.TextDecoding != nil {
		result.TextDecoding.DecodedBytes = len(text)
		result.TextDecoding.Truncated = projection.truncated
	}
	if projection.truncated {
		result.Partial = true
		result.Truncated = true
		result.Recovery = "use_dedicated_download_or_fulltext_tool"
		if result.Warning == "" {
			result.Warning = fmt.Sprintf("Decoded text exceeded the bounded projection limit of %d bytes; only the returned prefix is available and must not be treated as the complete source.", limit)
		}
	}
	result.Body = text
	return nil
}

func webResponseTextFailure(result *Result, body []byte, message string) {
	// Original bytes remain the sole digest and recovery authority, even when
	// a content or charset projection failed after producing some text.
	result.RawBodyBase64 = base64.StdEncoding.EncodeToString(body)
	result.SourceUnavailable = true
	result.Partial = true
	result.Recovery = "decode_original_response_or_use_source_specific_tool"
	if result.Error == "" {
		result.Error = message
	}
}
