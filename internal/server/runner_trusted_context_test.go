package server

import (
	"strings"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
)

func TestTrustedRuntimeContextRequiresAuditableReportReferences(t *testing.T) {
	context := sessionRunnerTrustedRuntimeContext(sessionstore.Session{}, nil)
	for _, marker := range []string{
		"Any user-visible report based on external evidence",
		"References or Evidence section",
		"retrievable source URLs or stable identifiers",
		"precise factual claims",
	} {
		if !strings.Contains(context, marker) {
			t.Fatalf("trusted runtime context is missing %q", marker)
		}
	}
}
