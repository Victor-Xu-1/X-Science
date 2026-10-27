package workspace

import (
	"encoding/json"
	"strings"
	"testing"

	"x-science/internal/memoryconfig"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestMemoryModeDecisionIsClosedAndPreservesLegacyPlanDigest(t *testing.T) {
	source := CompletedExtractionSource{Transcript: &transcriptstore.CompletedMemorySource{Attempt: 7}}
	input := ExtractionCommitInput{CursorBefore: 1, CursorNext: 2, AdvanceCursor: true, DeltaCount: 1, Gate: "done"}
	legacy, err := json.Marshal(struct {
		Plan                     ExtractionMutationPlan
		CursorBefore, CursorNext int
		AdvanceCursor, Overshoot bool
		DeltaCount               int
		Gate                     string
	}{input.Plan, 1, 2, true, false, 1, "done"})
	if err != nil {
		t.Fatal(err)
	}
	oldDigest, err := extractionPlanDigest(input)
	if err != nil || oldDigest != extractionDigest(legacy) {
		t.Fatal("absent metadata changed historical plan digest")
	}
	decision := &MemoryExtractionModeDecision{RequestedMode: "forked", EffectiveMode: "compact", Reason: MemoryModeForkedOversize, RecoveryPolicy: memoryconfig.ForkedViewRecoveryCompact, RequestAttempt: 7, ModelProfileSHA256: strings.Repeat("a", 64)}
	if decision.Validate(source) != nil {
		t.Fatal("valid explicit decision rejected")
	}
	input.ModeDecision = decision.Clone()
	withDecision, err := extractionPlanDigest(input)
	if err != nil || withDecision == oldDigest {
		t.Fatal("decision did not bind original plan identity")
	}
	for _, invalid := range []MemoryExtractionModeDecision{
		{RequestedMode: "forked", EffectiveMode: "compact", Reason: "provider-error", RecoveryPolicy: memoryconfig.ForkedViewRecoveryCompact, RequestAttempt: 7},
		{RequestedMode: "forked", EffectiveMode: "compact", Reason: MemoryModeForkedOversize, RecoveryPolicy: memoryconfig.ForkedViewRecoveryStrict, RequestAttempt: 7, ModelProfileSHA256: strings.Repeat("a", 64)},
		{RequestedMode: "forked", EffectiveMode: "compact", Reason: MemoryModeForkedOversize, RecoveryPolicy: memoryconfig.ForkedViewRecoveryCompact, RequestAttempt: 8, ModelProfileSHA256: strings.Repeat("a", 64)},
	} {
		if invalid.Validate(source) == nil {
			t.Fatal("invalid mode decision accepted")
		}
	}
	if (*MemoryExtractionModeDecision)(nil).Validate(source) != nil {
		t.Fatal("legacy unknown decision rejected")
	}
}
