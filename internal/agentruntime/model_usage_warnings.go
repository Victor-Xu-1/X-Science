package agentruntime

import (
	"encoding/json"
	"errors"
	"log"
)

// UsageCounterField is the fixed normalized counter domain.
type UsageCounterField uint8

const (
	UsageFieldInput UsageCounterField = iota
	UsageFieldOutput
	UsageFieldTotal
	UsageFieldCacheRead
	UsageFieldCacheWrite
	usageFieldCount
)

// ModelUsageWarnings retains at most five fields times two closed reasons.
// Unknown bits are ignored by every export; arbitrary strings are not accepted.
type ModelUsageWarnings uint16

const modelUsageWarningMask ModelUsageWarnings = (1 << (2 * usageFieldCount)) - 1

func modelUsageWarningNames() ([5]string, [2]string) {
	return [5]string{"input", "output", "total", "cache_read", "cache_write"},
		[2]string{"counter_type", "counter_range"}
}

type modelUsageWarningEntry struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (warnings ModelUsageWarnings) Empty() bool { return warnings&modelUsageWarningMask == 0 }

func (warnings ModelUsageWarnings) IsZero() bool { return warnings.Empty() }

func (warnings ModelUsageWarnings) Add(field UsageCounterField, reason UsageCounterReason) ModelUsageWarnings {
	if field >= usageFieldCount || (reason != UsageCounterType && reason != UsageCounterRange) {
		return warnings & modelUsageWarningMask
	}
	return (warnings | 1<<(2*field+UsageCounterField(reason-1))) & modelUsageWarningMask
}

func (warnings ModelUsageWarnings) Merge(next ModelUsageWarnings) ModelUsageWarnings {
	return (warnings | next) & modelUsageWarningMask
}

func (warnings ModelUsageWarnings) MarshalJSON() ([]byte, error) {
	// Only these literals can reach audit or diagnostic output, in stable order.
	fields, reasons := modelUsageWarningNames()
	entries := make([]modelUsageWarningEntry, 0, 2*usageFieldCount)
	for field, name := range fields {
		for reason, code := range reasons {
			if warnings&(1<<uint(2*field+reason)) != 0 {
				entries = append(entries, modelUsageWarningEntry{Field: name, Reason: code})
			}
		}
	}
	return json.Marshal(entries)
}

// UnmarshalJSON is only the diagnostic metadata codec, not a provider-decoding
// fallback. Unknown fixed-domain names are ignored for forward compatibility;
// malformed, oversized, or over-count lists fail without replacing prior state.
func (warnings *ModelUsageWarnings) UnmarshalJSON(raw []byte) error {
	if len(raw) > 1024 {
		return errors.New("model usage warning metadata exceeds 1024 bytes")
	}
	var entries []modelUsageWarningEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	if len(entries) > int(2*usageFieldCount) {
		return errors.New("model usage warning metadata exceeds 10 entries")
	}
	var next ModelUsageWarnings
	fields, reasons := modelUsageWarningNames()
	for _, entry := range entries {
		for field, name := range fields {
			for reason, code := range reasons {
				if entry.Field == name && entry.Reason == code {
					next = next.Add(UsageCounterField(field), UsageCounterReason(reason+1))
				}
			}
		}
	}
	*warnings = next
	return nil
}

// LogModelUsageWarnings is a request-settlement fallback when no audit sink is
// available. It neither changes the primary result nor emits stream activity.
func LogModelUsageWarnings(warnings ModelUsageWarnings) {
	if warnings.Empty() {
		return
	}
	raw, _ := warnings.MarshalJSON()
	log.Printf("model usage counter diagnostics: %s", raw)
}
