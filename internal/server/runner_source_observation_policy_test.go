package server

import (
	"testing"

	"x-science/internal/agentruntime"
)

func TestSourceObservationUnknownCannotProveUnchanged(t *testing.T) {
	for _, test := range []struct {
		name        string
		observation *agentruntime.SourceObservation
		nested      bool
		unchanged   bool
	}{
		{"incomplete verified group", &agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown, HasSources: true}, false, false},
		{"ordinary computation", &agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown}, false, false},
		{"verified repeated group", &agentruntime.SourceObservation{State: agentruntime.SourceObservationRepeated, HasSources: true}, false, true},
		{"new source", &agentruntime.SourceObservation{State: agentruntime.SourceObservationNew, HasSources: true}, false, false},
		{"nested receipt before outer completion", nil, true, true},
		{"no source observation", nil, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sourceObservationIsUnchanged(test.observation, test.nested); got != test.unchanged {
				t.Fatalf("unchanged=%t; want %t for %+v", got, test.unchanged, test.observation)
			}
		})
	}
}

func TestSourceReceiptSuccessUsesExistingOutcomeContract(t *testing.T) {
	for _, test := range []struct {
		name      string
		value     any
		succeeded bool
	}{
		{"empty successful search", map[string]any{"total_count": 0, "returned_count": 0}, true},
		{"success envelope", map[string]any{"ok": true, "records": []any{}}, true},
		{"unavailable source", map[string]any{"sourceUnavailable": true}, false},
		{"failed source", map[string]any{"ok": false}, false},
		{"partial source", map[string]any{"partial": true}, false},
		{"not executed", map[string]any{"ok": true, "executed": false}, false},
		{"nested unavailable envelope", map[string]any{"result": map[string]any{"sourceUnavailable": true}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runnerSourceReceiptSucceeded(test.value); got != test.succeeded {
				t.Fatalf("source success=%t; want %t", got, test.succeeded)
			}
		})
	}
}
