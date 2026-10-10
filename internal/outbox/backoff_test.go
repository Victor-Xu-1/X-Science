package outbox

import (
	"testing"
	"time"
)

func TestExponentialBackoffCapsAndNeverReturnsZero(t *testing.T) {
	base := 2 * time.Second
	maximum := 30 * time.Second
	if got := exponentialBackoff(base, maximum, 0); got != base {
		t.Fatalf("attempt 0 backoff=%s want=%s", got, base)
	}
	if got := exponentialBackoff(base, maximum, 1); got != base {
		t.Fatalf("attempt 1 backoff=%s want=%s", got, base)
	}
	if got := exponentialBackoff(base, maximum, 2); got != 2*base {
		t.Fatalf("attempt 2 backoff=%s want=%s", got, 2*base)
	}
	if got := exponentialBackoff(base, maximum, 10); got != maximum {
		t.Fatalf("attempt 10 backoff=%s want cap %s", got, maximum)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		got := exponentialBackoff(base, maximum, attempt)
		if got <= 0 || got > maximum {
			t.Fatalf("attempt %d backoff=%s out of bounds", attempt, got)
		}
	}
}
