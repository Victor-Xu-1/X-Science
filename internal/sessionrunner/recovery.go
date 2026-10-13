package sessionrunner

const (
	MinRecoverySemanticBytes int64 = 128
	MaxRecoveryStreak              = 4
)

// RecoveryProgress separates durable evidence from transport liveness. A new
// request or checkpoint alone cannot reopen an exhausted execution route.
type RecoveryProgress struct {
	SemanticBytes int64
	Consecutive   int
}

// AdvanceRecovery accounts for one failed generation of an unchanged route.
// Healthy output resets it; callers reset observations on actual tool effects.
// Budget proposals may change within the recovery window, but increasing a
// token allowance is not evidence that an unchanged route made progress.
func AdvanceRecovery(progress RecoveryProgress) (consecutive int, park bool) {
	if progress.SemanticBytes >= MinRecoverySemanticBytes {
		return 0, false
	}
	consecutive = progress.Consecutive + 1
	return consecutive, consecutive >= MaxRecoveryStreak
}
