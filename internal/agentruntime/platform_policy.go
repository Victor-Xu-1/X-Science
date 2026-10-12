package agentruntime

import (
	_ "embed"
	"strings"
)

//go:embed assets/x_science_platform_policy.md
var xsciencePlatformPolicy string

func XSciencePlatformPolicy() string {
	return strings.TrimSpace(xsciencePlatformPolicy)
}

func agentUsesXSciencePlatformPolicy(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "ONBOARDING", "REVIEWER", "BOOKMARKER":
		return false
	default:
		return true
	}
}
