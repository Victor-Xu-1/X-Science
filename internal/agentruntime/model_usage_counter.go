package agentruntime

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// UsageCounterState distinguishes an unavailable receipt from a measured zero.
type UsageCounterState uint8

const (
	UsageCounterMissing UsageCounterState = iota
	UsageCounterNull
	UsageCounterValid
	UsageCounterInvalid
)

// UsageCounterReason is a closed classification, never provider-supplied text.
type UsageCounterReason uint8

const (
	usageCounterNoReason UsageCounterReason = iota
	UsageCounterType
	UsageCounterRange
)

// OptionalUsageCounter is used only at declared optional numeric usage leaves.
// Its zero value is missing; invalid telemetry never retains its raw value.
type OptionalUsageCounter struct {
	value  int
	state  UsageCounterState
	reason UsageCounterReason
}

func (counter *OptionalUsageCounter) UnmarshalJSON(raw []byte) error {
	*counter = OptionalUsageCounter{}
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) {
		var value json.RawMessage
		return json.Unmarshal(raw, &value)
	}
	if bytes.Equal(raw, []byte("null")) {
		counter.state = UsageCounterNull
		return nil
	}
	counter.state, counter.reason = UsageCounterInvalid, UsageCounterType
	if (raw[0] < '0' || raw[0] > '9') && raw[0] != '-' {
		return nil
	}
	if bytes.ContainsAny(raw, ".eE") {
		return nil
	}
	value, err := strconv.ParseInt(string(raw), 10, strconv.IntSize)
	if err != nil || value < 0 {
		counter.reason = UsageCounterRange
		return nil
	}
	counter.value, counter.state, counter.reason = int(value), UsageCounterValid, usageCounterNoReason
	return nil
}

func (counter OptionalUsageCounter) State() UsageCounterState   { return counter.state }
func (counter OptionalUsageCounter) Reason() UsageCounterReason { return counter.reason }

func (counter OptionalUsageCounter) Value() (int, bool) {
	return counter.value, counter.state == UsageCounterValid
}
