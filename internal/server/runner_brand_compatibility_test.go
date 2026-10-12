package server

import (
	"strings"
	"testing"

	"synon-go/internal/agentruntime"
)

func TestCurrentRecoveryBrandReadsImmutableLegacyMarkers(t *testing.T) {
	if !strings.HasPrefix(sessionRunnerDurableCorrectionContextMarker, "X-Science ") {
		t.Fatal("new recovery prompt retains legacy branding")
	}
	for _, marker := range []string{sessionRunnerDurableCorrectionContextMarker, legacyDurableCorrectionContextMarker} {
		messages := []agentruntime.Message{{Role: "system", Content: marker}}
		if !hasSessionRunnerDurableCorrectionContext(marker) || runnerCorrectionToolBoundaryIndex(messages) != 0 {
			t.Fatalf("recovery boundary lost exact current/legacy marker %q", marker)
		}
	}
	state := newSessionRunnerNoProgressRecovery(strings.Repeat("a", 64))
	state.Consecutive = 2
	current := state.resumeDetail("same obligation")
	if !strings.Contains(current, sessionRunnerNoProgressDetailMarker) || strings.Contains(current, legacyNoProgressDetailMarker) {
		t.Fatal("new checkpoint did not use current brand")
	}
	legacy := strings.Replace(current, sessionRunnerNoProgressDetailMarker, legacyNoProgressDetailMarker, 1)
	for _, detail := range []string{current, legacy} {
		recovered, ok := parseSessionRunnerNoProgressRecoveryDetail(detail)
		if !ok || recovered.Consecutive != 2 || recovered.ObligationFingerprint != state.ObligationFingerprint {
			t.Fatalf("immutable checkpoint failed exact read: %#v %t", recovered, ok)
		}
		if correctionRepetitionFingerprint("same", detail) != correctionRepetitionFingerprint("same", "same obligation") {
			t.Fatal("branding changed the correction obligation fingerprint")
		}
	}
	// As before, the latest suffix is authoritative; a malformed later suffix
	// cannot silently fall back to an earlier valid checkpoint of either brand.
	for _, detail := range []string{current + legacyNoProgressDetailMarker + "{}", legacy + sessionRunnerNoProgressDetailMarker + "{}"} {
		if _, ok := parseSessionRunnerNoProgressRecoveryDetail(detail); ok {
			t.Fatal("malformed latest checkpoint weakened recovery validation")
		}
	}
}
