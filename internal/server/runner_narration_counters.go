package server

import "unicode"

// These constant-space counters describe the response boundary, not correctness.
// Repetition can be intentional (data, sequences, quotations); it must never
// become a task verdict, retry trigger, or a reason to retain the underlying text.
func addSessionRunnerNarrationCounters(record map[string]any, prefix, text string) {
	units, longest, run, start := 0, 0, 0, -1
	previous := ""
	finish := func(end int) {
		if start < 0 {
			return
		}
		unit := text[start:end]
		units++
		if unit == previous {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		previous, start = unit, -1
	}
	for offset, value := range text {
		if unicode.IsSpace(value) {
			finish(offset)
		} else if start < 0 {
			start = offset
		}
	}
	finish(len(text))
	record[prefix+"whitespace_units"] = units
	record[prefix+"longest_equal_unit_run"] = longest
}
