package server

import (
	"math"
	"testing"
	"time"
)

func TestComputeJobTimeoutDoesNotImposeTaskLifetime(t *testing.T) {
	for _, input := range []map[string]any{{}, {"timeout_seconds": 0}, {"timeout_seconds": 172800}} {
		got, err := computeJobTimeout(input, nil, 0)
		want := time.Duration(numberValue(input["timeout_seconds"])) * time.Second
		if err != nil || got != want {
			t.Fatalf("input=%v timeout=%s want=%s err=%v", input, got, want, err)
		}
	}
}

func TestComputeJobTimeoutPreservesRealAuthority(t *testing.T) {
	configured := 7200
	for _, input := range []map[string]any{{}, {"timeout_seconds": 0}} {
		got, err := computeJobTimeout(input, &configured, 24*time.Hour)
		if err != nil || got != 2*time.Hour {
			t.Fatalf("configured limit lost: timeout=%s err=%v", got, err)
		}
	}
	if _, err := computeJobTimeout(map[string]any{"timeout_seconds": 172800}, nil, 24*time.Hour); err == nil {
		t.Fatal("physical provider lifetime silently overridden or request shortened")
	}
	for _, value := range []any{-1, 0.5, 1e-12, math.NaN(), math.Inf(1), true, "3600", float64(math.MaxInt64)} {
		if _, err := computeJobTimeout(map[string]any{"timeout_seconds": value}, nil, 0); err == nil {
			t.Fatalf("invalid timeout accepted: %#v", value)
		}
	}
	negative := -1
	if _, err := computeJobTimeout(nil, &negative, 0); err == nil {
		t.Fatal("invalid operator authority accepted")
	}
}
