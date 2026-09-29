package sessionrunner

import "testing"

func TestRecoveryMeasuresEffectsRatherThanLiveness(t *testing.T) {
	for _, test := range []struct {
		name  string
		input RecoveryProgress
		count int
		park  bool
	}{
		{"fragment", RecoveryProgress{SemanticBytes: 3, Consecutive: 3}, 4, true},
		{"empty", RecoveryProgress{Consecutive: 3}, 4, true},
		{"healthy output", RecoveryProgress{SemanticBytes: 128, Consecutive: 3}, 0, false},
		{"new budget", RecoveryProgress{BudgetCanGrow: true, Consecutive: 3}, 4, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			count, park := AdvanceRecovery(test.input)
			if count != test.count || park != test.park {
				t.Fatalf("count=%d park=%t", count, park)
			}
		})
	}
}
