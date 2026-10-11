package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRetryOwnedGrantStoreContentionSurvivesMoreThanFourBusyErrors(t *testing.T) {
	attempts := 0
	err := retryOwnedGrantStoreContention(context.Background(), "runner_lease_renewal", time.Millisecond, func() error {
		attempts++
		if attempts <= 6 {
			return errors.New("SQLITE_BUSY")
		}
		return nil
	})
	if err != nil || attempts != 7 {
		t.Fatalf("owned grant stopped after busy burst: attempts=%d err=%v", attempts, err)
	}
}

func TestRetryTransientStoreContentionStillCapsPreparationRetries(t *testing.T) {
	attempts := 0
	err := retryTransientStoreContention(context.Background(), "load_reviewer_parent", func() error {
		attempts++
		return errors.New("SQLITE_BUSY")
	})
	if attempts != sessionRunnerStoreContentionMaxAttempts {
		t.Fatalf("preparation retry count=%d", attempts)
	}
	if err == nil || !strings.Contains(err.Error(), "exhausted 4 transient store attempts") {
		t.Fatalf("preparation error=%v", err)
	}
}

func TestRetryOwnedGrantStoreContentionStopsOnPermanentError(t *testing.T) {
	attempts := 0
	want := errors.New("schema is unavailable")
	err := retryOwnedGrantStoreContention(context.Background(), "runner_lease_renewal", time.Millisecond, func() error {
		attempts++
		return want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("permanent error retried: attempts=%d err=%v", attempts, err)
	}
}

func TestRetryOwnedGrantStoreContentionStopsWhenGrantExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := retryOwnedGrantStoreContention(ctx, "runner_lease_renewal", 5*time.Millisecond, func() error {
		return errors.New("SQLITE_BUSY")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("grant expiry error=%v", err)
	}
	if strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("owned grant used preparation exhaustion: %v", err)
	}
}

func TestRetryOwnedGrantStoreContentionHonorsParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := retryOwnedGrantStoreContention(ctx, "runner_lease_renewal", time.Millisecond, func() error {
		t.Fatal("cancelled grant still attempted a store write")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancel error=%v", err)
	}
}
