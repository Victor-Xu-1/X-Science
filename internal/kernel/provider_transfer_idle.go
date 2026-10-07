package kernel

import (
	"bytes"
	"context"
	"errors"
	"time"
)

var errProviderTransferIdle = errors.New("provider output transfer has no observed progress; partial data retained")

// This owns a read-only transfer attempt, never the remote computation. The
// progress file is written only when payload/verification bytes advance.
func providerTransferContext(parent context.Context, stage string, idle time.Duration) (context.Context, context.CancelFunc) {
	if idle <= 0 {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		ticker := time.NewTicker(min(time.Second, max(time.Millisecond, idle/4)))
		defer ticker.Stop()
		last := time.Now()
		var previous []byte
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				raw, found, err := readBoundedKernelMetadataFile(stage, ".transfer-progress.json", 1024)
				if err == nil && found && !bytes.Equal(raw, previous) {
					previous = append(previous[:0], raw...)
					last = time.Now()
				}
				if time.Since(last) >= idle {
					cancel(errProviderTransferIdle)
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}
