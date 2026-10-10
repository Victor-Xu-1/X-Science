package agentruntime

import "testing"

func TestAgentUsesSynonPlatformPolicyExcludesHiddenSupportAgents(t *testing.T) {
	for _, name := range []string{"ONBOARDING", "REVIEWER", "BOOKMARKER", "onboarding", " reviewer "} {
		if agentUsesSynonPlatformPolicy(name) {
			t.Fatalf("hidden support agent %q should not receive platform policy", name)
		}
	}
	for _, name := range []string{"OPERON", "AIDD_EXPERT", "MEDCHEM_EXPERT", " operon "} {
		if !agentUsesSynonPlatformPolicy(name) {
			t.Fatalf("scientific agent %q should receive platform policy", name)
		}
	}
}
