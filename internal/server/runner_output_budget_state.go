package server

// State is a hint for a future request, not a claimed model maximum. Rejection
// bounds apply only to the exact generation context that produced them.
type outputBudgetState struct {
	Next                    int    `json:"next"`
	Lower                   int    `json:"lower"`
	Rejected                int    `json:"rejected"`
	Ceiling                 int    `json:"ceiling"`
	Context                 string `json:"context"`
	Owner                   string `json:"ownerUserId"`
	Session                 string `json:"sessionId"`
	SkipInitialAllowance    bool   `json:"skipInitialAllowance,omitempty"`
	DefaultProbePending     bool   `json:"defaultProbePending,omitempty"`
	DefaultProbeUsed        bool   `json:"defaultProbeUsed,omitempty"`
	DefaultProbeFromInitial bool   `json:"defaultProbeFromInitial,omitempty"`
}

// Preserve the provider error and the scope of a bounded recovery decision.
// Saturation is not permission to replay an unproductive generation forever.
type sessionOutputBudgetSaturatedError struct {
	cause error
	scope string
}

func (e *sessionOutputBudgetSaturatedError) Error() string { return e.cause.Error() }
func (e *sessionOutputBudgetSaturatedError) Unwrap() error { return e.cause }

type sessionOutputBudgetRecoveryError struct {
	cause   error
	scope   string
	canGrow bool
}

func (e *sessionOutputBudgetRecoveryError) Error() string { return e.cause.Error() }
func (e *sessionOutputBudgetRecoveryError) Unwrap() error { return e.cause }
