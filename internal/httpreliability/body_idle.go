package httpreliability

import (
	"errors"
	"io"
	"sync"
	"time"
)

type idleBody struct {
	body    io.ReadCloser
	timeout time.Duration
}

func (body *idleBody) Read(target []byte) (int, error) {
	return readWithIdleDeadline(body.body, target, func(expire func()) func() bool {
		return time.AfterFunc(body.timeout, expire).Stop
	})
}

func (body *idleBody) Close() error { return body.body.Close() }

// The arm function isolates scheduling, not timeout policy. Production uses
// time.AfterFunc; deterministic ordering tests exercise this same callback.
func readWithIdleDeadline(body io.ReadCloser, target []byte, arm func(func()) func() bool) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	var mu sync.Mutex
	finished, expired := false, false
	timerDone := make(chan struct{})
	stop := arm(func() {
		mu.Lock()
		expired = !finished
		mu.Unlock()
		if expired {
			_ = body.Close()
		}
		close(timerDone)
	})
	count, err := body.Read(target)
	mu.Lock()
	finished = true
	mu.Unlock()
	if !stop() {
		// A committed close finishes before another read can start.
		<-timerDone
	}
	if !expired {
		return count, err
	}
	// Returned bytes and eventual EOF do not erase a deadline that already
	// committed. Preserve both the causal timeout and the underlying outcome.
	return count, errors.Join(ErrBodyIdleTimeout, err)
}
