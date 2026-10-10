package sessionrunner

import "testing"

func TestAdvanceRecoveryParksOnlyExhaustedUnchangedRoutes(t *testing.T) {
	cases := []struct {
		name  string
		input RecoveryProgress
		count int
		park  bool
	}{
		{"just below evidence floor", RecoveryProgress{SemanticBytes: MinRecoverySemanticBytes - 1, Consecutive: MaxRecoveryStreak - 1}, MaxRecoveryStreak, true},
		{"exact evidence floor resets", RecoveryProgress{SemanticBytes: MinRecoverySemanticBytes, Consecutive: MaxRecoveryStreak - 1}, 0, false},
		{"first empty attempt stays open", RecoveryProgress{}, 1, false},
		{"third empty attempt stays open", RecoveryProgress{Consecutive: MaxRecoveryStreak - 2}, MaxRecoveryStreak - 1, false},
		{"fourth empty attempt parks", RecoveryProgress{Consecutive: MaxRecoveryStreak - 1}, MaxRecoveryStreak, true},
		{"growing budget never parks", RecoveryProgress{BudgetCanGrow: true, Consecutive: 100}, 101, false},
		{"healthy output ignores prior streak", RecoveryProgress{SemanticBytes: MinRecoverySemanticBytes + 64, Consecutive: 20}, 0, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			count, park := AdvanceRecovery(testCase.input)
			if count != testCase.count || park != testCase.park {
				t.Fatalf("count=%d park=%t want count=%d park=%t", count, park, testCase.count, testCase.park)
			}
		})
	}
}
