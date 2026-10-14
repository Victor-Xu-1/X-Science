package common

import (
	"context"
	"errors"
)

// Close stops admission, cancels delivery and waits for all admitted workers
// and Enqueue result callbacks, including workers already removed by Unbind.
// A timeout does not mean quiescence; another Close may continue the same wait.
// It does not flush/ack queued messages or claim that every delivery succeeded.
func (d *SessionOutboundDispatcher) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("session outbound close context is required")
	}
	if d == nil {
		return nil
	}
	d.mu.Lock()
	if d.done == nil {
		d.done = make(chan struct{})
	}
	if !d.closing {
		d.closing = true
		if d.cancel != nil {
			d.cancel()
		}
		if d.active == 0 {
			close(d.done)
		}
	}
	done := d.done
	d.mu.Unlock()
	// Prefer an already completed close even for a cancelled caller. Recheck
	// after cancellation wins the select so real completion is never hidden.
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		select {
		case <-done:
			return nil
		default:
			return ctx.Err()
		}
	}
}

func (d *SessionOutboundDispatcher) finishSessionOutboundActivity() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.active--
	if d.closing && d.active == 0 {
		close(d.done)
	}
}
