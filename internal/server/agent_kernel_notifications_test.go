package server

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestAgentKernelNotificationClaimCountValidatesEmptyAndReceivedManifests(t *testing.T) {
	for _, status := range []string{"idle", "completed", "timeout", "error", "uncollected_results"} {
		t.Run(status, func(t *testing.T) {
			for _, manifest := range []struct {
				count     int
				items     string
				wantError bool
			}{
				{0, "[]", false}, {1, "[]", true}, {0, "[{}]", true}, {-1, "[]", true},
			} {
				raw := fmt.Sprintf(`{"status":%q,"num_notifications":%d,"notifications":%s}`, status, manifest.count, manifest.items)
				count, err := agentKernelNotificationClaimCount(raw)
				if (err != nil) != manifest.wantError || count != 0 {
					t.Fatalf("manifest=%s count=%d err=%v", raw, count, err)
				}
			}
		})
	}
	for _, raw := range []string{
		`{"status":"received","num_notifications":0,"notifications":[]}`,
		`{"status":"received","num_notifications":2,"notifications":[{}]}`,
		`{"status":"unknown","num_notifications":0,"notifications":[]}`,
		`invalid`,
	} {
		if _, err := agentKernelNotificationClaimCount(raw); err == nil {
			t.Fatalf("invalid manifest accepted: %s", raw)
		}
	}
	if count, err := agentKernelNotificationClaimCount(`{"status":"received","num_notifications":1,"notifications":[{"id":"receipt-1"}]}`); err != nil || count != 1 {
		t.Fatalf("valid received manifest count=%d err=%v", count, err)
	}
}

func TestAgentKernelNotificationTimeoutUsesBoundedWaitContract(t *testing.T) {
	tests := []struct {
		name      string
		input     map[string]any
		want      time.Duration
		wantError bool
	}{
		{name: "default", input: map[string]any{}, want: 30 * time.Second},
		{name: "peek", input: map[string]any{"timeout_seconds": float64(0)}, want: 0},
		{name: "negative peek", input: map[string]any{"timeout_seconds": float64(-1)}, want: 0},
		{name: "fractional", input: map[string]any{"timeout_seconds": float64(12.5)}, want: 12500 * time.Millisecond},
		{name: "exact maximum", input: map[string]any{"timeout_seconds": float64(1800)}, want: 30 * time.Minute},
		{name: "over maximum", input: map[string]any{"timeout_seconds": float64(1801)}, want: 30 * time.Minute},
		{name: "positive infinity", input: map[string]any{"timeout_seconds": math.Inf(1)}, want: 30 * time.Minute},
		{name: "wrong type", input: map[string]any{"timeout_seconds": "30"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := agentKernelNotificationTimeoutDuration(test.input)
			if (err != nil) != test.wantError {
				t.Fatalf("timeout=%s err=%v", got, err)
			}
			if err == nil && got != test.want {
				t.Fatalf("timeout=%s want=%s", got, test.want)
			}
		})
	}
}
