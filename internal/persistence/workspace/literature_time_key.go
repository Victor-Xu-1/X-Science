package workspace

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// Use the original driver's package-initialization lifecycle so every existing
// pool connection sees the same deterministic function. Unlike search text,
// this saved error is checked only by explicit date reads, never Store.Open.
var literatureTimeKeyRegistrationError = sqlite.RegisterDeterministicScalarFunction("xscience_literature_time_key_v1", 1,
	func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		value, ok := args[0].(string)
		if !ok {
			return nil, nil
		}
		key, known := literatureTimeKey(value)
		if !known {
			return nil, nil
		}
		return key, nil
	})

// The first layout is modernc/sqlite's default time.Time.String() binding;
// the rest cover explicit RFC3339 offsets and its documented SQLite formats.
// Naive dates are UTC. Numeric epoch strings have no unambiguous unit.
func literatureTimeKey(value string) (string, bool) {
	value = strings.TrimSpace(value)
	// time.Parse otherwise truncates excess fractional digits. Unsupported
	// precision stays unknown rather than silently coalescing distinct dates.
	if index := strings.IndexAny(value, ".,"); index >= 0 {
		digits := 0
		for index++; index < len(value) && value[index] >= '0' && value[index] <= '9'; index++ {
			digits++
		}
		if digits > 9 {
			return "", false
		}
	}
	for _, layout := range [...]string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04",
		"2006-01-02T15:04",
		"2006-01-02",
	} {
		instant, err := time.Parse(layout, value)
		if err == nil {
			// Flipping the signed seconds' sign bit preserves chronological
			// byte order across the full parsed date range; nanos stay separate.
			// Do not return time.Time (the driver converts UDF times to seconds)
			// or use UnixNano, which overflows outside roughly 1678..2262.
			return fmt.Sprintf("%016x%09d", uint64(instant.Unix())^(uint64(1)<<63), instant.Nanosecond()), true
		}
	}
	return "", false
}
