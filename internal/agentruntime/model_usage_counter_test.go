package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOptionalUsageCounterStates(t *testing.T) {
	maximum := strconv.FormatUint(uint64(^uint(0)>>1), 10)
	overflow := strconv.FormatUint(uint64(^uint(0)>>1)+1, 10)
	for _, test := range []struct {
		name, raw string
		state     UsageCounterState
		reason    UsageCounterReason
		value     int
	}{
		{"missing", `{}`, UsageCounterMissing, usageCounterNoReason, 0},
		{"null", `{"count":null}`, UsageCounterNull, usageCounterNoReason, 0},
		{"zero", `{"count":0}`, UsageCounterValid, usageCounterNoReason, 0},
		{"value", `{"count":21}`, UsageCounterValid, usageCounterNoReason, 21},
		{"maximum", `{"count":` + maximum + `}`, UsageCounterValid, usageCounterNoReason, int(^uint(0) >> 1)},
		{"string", `{"count":"private-fixture-value"}`, UsageCounterInvalid, UsageCounterType, 0},
		{"fraction", `{"count":1.5}`, UsageCounterInvalid, UsageCounterType, 0},
		{"exponent", `{"count":1e2}`, UsageCounterInvalid, UsageCounterType, 0},
		{"bool", `{"count":true}`, UsageCounterInvalid, UsageCounterType, 0},
		{"array", `{"count":[1]}`, UsageCounterInvalid, UsageCounterType, 0},
		{"object", `{"count":{"value":1}}`, UsageCounterInvalid, UsageCounterType, 0},
		{"negative", `{"count":-1}`, UsageCounterInvalid, UsageCounterRange, 0},
		{"overflow", `{"count":` + overflow + `}`, UsageCounterInvalid, UsageCounterRange, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var wire struct {
				Count OptionalUsageCounter `json:"count"`
			}
			if err := json.Unmarshal([]byte(test.raw), &wire); err != nil {
				t.Fatal(err)
			}
			value, valid := wire.Count.Value()
			if wire.Count.State() != test.state || wire.Count.Reason() != test.reason ||
				value != test.value || valid != (test.state == UsageCounterValid) {
				t.Fatalf("counter state=%d reason=%d value=%d valid=%t", wire.Count.State(), wire.Count.Reason(), value, valid)
			}
		})
	}
	var counter OptionalUsageCounter
	for _, raw := range []string{`"private-fixture-value"`, `0`, `null`} {
		if err := json.Unmarshal([]byte(raw), &counter); err != nil {
			t.Fatal(err)
		}
	}
	if counter.State() != UsageCounterNull || counter.Reason() != usageCounterNoReason {
		t.Fatal("new null value retained old invalid state")
	}
	if err := json.Unmarshal([]byte(`{"count":`), &struct {
		Count OptionalUsageCounter `json:"count"`
	}{}); err == nil {
		t.Fatal("invalid JSON syntax was softened")
	}
}

func TestModelUsageWarningsClosedAndBounded(t *testing.T) {
	var warnings ModelUsageWarnings
	for field := UsageFieldInput; field < usageFieldCount; field++ {
		warnings = warnings.Add(field, UsageCounterType).Add(field, UsageCounterRange)
	}
	raw, err := json.Marshal(warnings.Merge(ModelUsageWarnings(1 << 15)))
	if err != nil || len(raw) >= 1024 {
		t.Fatalf("warning bytes=%d err=%v", len(raw), err)
	}
	var entries []modelUsageWarningEntry
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) != 10 {
		t.Fatalf("warning entries=%d err=%v", len(entries), err)
	}
	var restored ModelUsageWarnings
	if err := json.Unmarshal(raw, &restored); err != nil || restored != warnings {
		t.Fatalf("warning round-trip=%d err=%v", restored, err)
	}
	if err := json.Unmarshal([]byte(`[{"field":"input","reason":"counter_type"},{"field":"private-fixture-value","reason":"counter_range"},{"field":"output","reason":"private-fixture-value"}]`), &restored); err != nil || restored != ModelUsageWarnings(0).Add(UsageFieldInput, UsageCounterType) {
		t.Fatalf("unknown warning names entered state: %d err=%v", restored, err)
	}
	before := restored
	for _, bad := range []string{`[{"field":"` + strings.Repeat("x", 1025) + `","reason":"counter_type"}]`, `[{"field":true}]`, `[`, `[` + strings.Repeat(`{},`, 10) + `{}]`} {
		if err := json.Unmarshal([]byte(bad), &restored); err == nil || restored != before {
			t.Fatal("invalid warning metadata accepted or replaced prior state")
		}
	}
	if err := json.Unmarshal([]byte(`null`), &restored); err != nil || !restored.Empty() {
		t.Fatal("null warning metadata did not clear state")
	}
	if !ModelUsageWarnings(1<<15).Empty() || !ModelUsageWarnings(0).Add(UsageCounterField(255), UsageCounterReason(255)).Empty() {
		t.Fatal("unknown warning bits or domains were exported")
	}
	var output bytes.Buffer
	priorOutput, priorFlags, priorPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() { log.SetOutput(priorOutput); log.SetFlags(priorFlags); log.SetPrefix(priorPrefix) }()
	LogModelUsageWarnings(0)
	LogModelUsageWarnings(ModelUsageWarnings(1 << 15))
	if output.Len() != 0 {
		t.Fatal("empty warnings wrote diagnostics")
	}
	LogModelUsageWarnings(warnings)
	if strings.Count(output.String(), "\n") != 1 || output.Len() >= 1024 || strings.Contains(output.String(), "private-fixture-value") {
		t.Fatal("diagnostic was not one bounded closed record")
	}
	encoded, err := json.Marshal(ModelUsage{})
	if err != nil || bytes.Contains(encoded, []byte(`"warnings"`)) || bytes.Contains(encoded, []byte(`"invalid_fields"`)) {
		t.Fatal("ordinary usage gained empty diagnostic fields")
	}
}

func TestModelUsageDerivationValidity(t *testing.T) {
	decode := func(raw string) OptionalUsageCounter {
		var counter OptionalUsageCounter
		if err := json.Unmarshal([]byte(raw), &counter); err != nil {
			t.Fatal(err)
		}
		return counter
	}
	empty := OptionalUsageCounter{}
	input, output, zero, invalid := decode(`20`), decode(`3`), decode(`0`), decode(`"untrusted"`)
	for _, test := range []struct {
		name         string
		cache        OptionalUsageCounter
		additive     bool
		total        int
		invalidCache bool
	}{
		{"overlapping-invalid-cache", invalid, false, 23, true},
		{"additive-invalid-cache", invalid, true, 0, true},
		{"additive-zero-cache", zero, true, 23, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			usage := ModelUsageFromCounters(input, output, empty, test.cache, empty)
			usage.DeriveTotal(test.additive)
			if usage.TotalTokens != test.total || usage.Reported.Total || !usage.Reported.Input || !usage.Reported.Output || usage.InvalidFields.CacheRead != test.invalidCache {
				t.Fatalf("usage=%+v", usage)
			}
		})
	}
	maximum := decode(strconv.FormatUint(uint64(^uint(0)>>1), 10))
	usage := ModelUsageFromCounters(maximum, output, empty, empty, empty)
	usage.DeriveTotal(false)
	if usage.TotalTokens != 0 || usage.Warnings.Empty() || usage.Reported.Total {
		t.Fatal("overflow became an authoritative total")
	}
	usage = ModelUsageFromCounters(input, output, invalid, empty, empty)
	usage.DeriveTotal(false)
	if usage.TotalTokens != 0 || !usage.InvalidFields.Total || usage.Reported.Total {
		t.Fatal("invalid explicit total was silently replaced")
	}
}

func TestOpenAIChatClientOptionalUsageCounters(t *testing.T) {
	for _, test := range []struct {
		name    string
		counter any
		primary bool
	}{
		{"string", "private-fixture-value", false}, {"overflow", json.Number("9223372036854775808"), false},
		{"zero", 0, false}, {"invalid-primary-id", 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var id any = "direct-usage"
				if test.primary {
					id = 17
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"id": id, "model": "fixture", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": test.counter}}); err != nil {
					t.Error(err)
				}
			}))
			defer provider.Close()
			client := OpenAIChatClient{Endpoint: provider.URL, Model: "fixture", HTTPClient: provider.Client(), RequestTimeout: time.Second, MaxAttempts: 1}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			response, err := client.Complete(ctx, ModelRequest{})
			if calls.Load() != 1 {
				t.Fatalf("calls=%d", calls.Load())
			}
			if test.primary {
				var typeError *json.UnmarshalTypeError
				if !errors.As(err, &typeError) || typeError.Field != "id" {
					t.Fatalf("malformed primary identity was softened: %v", err)
				}
				return
			}
			if err != nil || response.Message.Role != "assistant" || response.Message.Content != "done" ||
				response.RequestID != "direct-usage" || response.Model != "fixture" || response.StopReason != "stop" ||
				len(response.Message.ToolCalls) != 0 || response.Usage.InputTokens != 20 || !response.Usage.ReportedFields().Input {
				t.Fatalf("response=%+v err=%v", response, err)
			}
			invalid := test.name != "zero"
			wantTotal := 20
			if invalid {
				wantTotal = 0
			}
			if response.Usage.InvalidFields.Output != invalid || !response.Usage.Warnings.Empty() != invalid ||
				response.Usage.ReportedFields().Output != !invalid || response.Usage.OutputTokens != 0 || response.Usage.TotalTokens != wantTotal {
				t.Fatalf("usage=%+v", response.Usage)
			}
		})
	}
}
