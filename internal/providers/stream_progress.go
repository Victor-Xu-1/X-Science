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
	reader.mu.Lock()
	if reader.awaitingGrowth && growth <= 0 {
		reader.mu.Unlock()
		return
	}
	reader.awaitingGrowth = false
	reader.mu.Unlock()
	if growth > 0 && reader.decodedGrowth != nil {
		reader.decodedGrowth(growth)
	}
	reader.reset()
}

// Error bodies and non-SSE fallbacks have no incremental protocol decoder.
// With an initial-growth owner, unframed bytes must not start a competing idle
// timer. Plain/error bodies retain their bounded transport-idle cancellation.
func readProviderStreamResponseBody(body io.Reader, limit int64, timeout time.Duration, cancel context.CancelCauseFunc, initialGrowth func(int64)) ([]byte, error) {
	reader := newOpenAIChatStreamIdleReader(body, timeout, cancel, initialGrowth)
	reader.semanticProgress = initialGrowth != nil
	defer reader.Stop()
	return readBoundedProviderBody(reader, limit)
}
