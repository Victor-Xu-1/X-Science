package memoryconfig

import (
	"encoding/json"
	"errors"
)

// ForkedViewRecovery is a semantic user policy, never an increased size limit.
type ForkedViewRecovery string

const (
	ForkedViewRecoveryStrict  ForkedViewRecovery = "strict"
	ForkedViewRecoveryCompact ForkedViewRecovery = "compact-on-oversize"
)

func (policy ForkedViewRecovery) Valid() bool {
	return policy == ForkedViewRecoveryStrict || policy == ForkedViewRecoveryCompact
}

// A zero typed value from an old caller is conservative, just like old JSON.
func (policy ForkedViewRecovery) Effective() ForkedViewRecovery {
	if policy == "" {
		return ForkedViewRecoveryStrict
	}
	return policy
}

func (policy *ForkedViewRecovery) UnmarshalJSON(raw []byte) error {
	var value string
	if policy == nil || json.Unmarshal(raw, &value) != nil || !ForkedViewRecovery(value).Valid() {
		return errors.New("memory.forked_view_recovery must be strict or compact-on-oversize")
	}
	*policy = ForkedViewRecovery(value)
	return nil
}
