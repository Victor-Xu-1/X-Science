package providers

import (
	"context"
	"io"
	"time"
)

// Decoder-confirmed progress renews a streaming request. SSE comments and
// repeated transport heartbeats cannot keep a stalled generation alive.
func recordProviderStreamProgress(reader io.Reader, growth int64) {
	if progress, ok := reader.(interface{ recordProgress(int64) }); ok {
		progress.recordProgress(growth)
	}
}

func (reader *openAIChatStreamIdleReader) recordProgress(growth int64) {
	reader.reset()
	if growth > 0 && reader.decodedGrowth != nil {
		reader.decodedGrowth(growth)
	}
}

// Error bodies and non-SSE fallbacks have no incremental protocol decoder.
// Their bounded byte reader still needs transport-idle cancellation.
func readProviderStreamResponseBody(body io.Reader, limit int64, timeout time.Duration, cancel context.CancelCauseFunc) ([]byte, error) {
	reader := newOpenAIChatStreamIdleReader(body, timeout, cancel)
	reader.semanticProgress = false
	defer reader.Stop()
	return readBoundedProviderBody(reader, limit)
}
