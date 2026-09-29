package sessionrunner

const (
	MinRecoverySemanticBytes int64 = 128
	MaxRecoveryStreak              = 4
)

// RecoveryProgress separates durable evidence from transport liveness. A new
// request or checkpoint alone cannot reopen an exhausted execution route.
type RecoveryProgress struct {
	SemanticBytes int64
	BudgetCanGrow bool
	Consecutive   int
}

// AdvanceRecovery accounts for one failed generation of an unchanged route.
// Healthy output resets it; callers reset observations on actual tool effects.
// Larger effective budgets remain
// recoverable without pretending they already produced evidence.
func AdvanceRecovery(progress RecoveryProgress) (consecutive int, park bool) {
	if progress.SemanticBytes >= MinRecoverySemanticBytes {
		return 0, false
	}
	consecutive = progress.Consecutive + 1
	return consecutive, consecutive >= MaxRecoveryStreak && !progress.BudgetCanGrow
}
