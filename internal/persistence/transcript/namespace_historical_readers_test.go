package transcript

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNamespaceHistoricalConditionKeepsOriginalFingerprintAndCanonicalNewWrites(t *testing.T) {
	raw := []byte(`{"schema":"synon.runner_correction_condition.v1","reason_code":"transient_provider_error","text":{"detail":"retained evidence"}}`)
	if _, err := DecodeRunnerCorrectionCondition(raw); err == nil {
		t.Fatal("new admission accepted historical schema")
	}
	condition, err := decodeHistoricalRunnerCorrectionCondition(raw)
	if err != nil || condition.Schema != RunnerCorrectionConditionSchema {
		t.Fatalf("historical condition=%#v err=%v", condition, err)
	}
	var original RunnerCorrectionCondition
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if condition.Fingerprint() != original.Fingerprint() || condition.ContentID() != original.ContentID() {
		t.Fatal("historical content/fingerprint identity changed")
	}
	cause, encoded, err := prepareRunnerCorrectionStorage("new-native", &RunnerInterruptionCause{ReasonCode: condition.ReasonCode, Detail: "retained", Condition: &condition})
	if err != nil || strings.Contains(string(encoded), "synon") {
		t.Fatalf("new publication=%s err=%v", encoded, err)
	}
	canonical, err := DecodeRunnerCorrectionCondition(encoded)
	if err != nil || cause.ConditionRef == nil || cause.ConditionRef.Fingerprint != canonical.Fingerprint() {
		t.Fatalf("new ref does not bind new schema: cause=%#v err=%v", cause, err)
	}
	if condition.ContentID() != original.ContentID() {
		t.Fatal("new publication changed the historical reader value")
	}
}

func TestNamespaceHistoricalInterruptionReaderIsStrict(t *testing.T) {
	for _, schema := range []string{"synon.runner_interruption_cause.v1", "synon.runner_interruption_cause.v2"} {
		record := map[string]any{"schema": schema, "reason_code": "transient_provider_error", "detail": "retained"}
		if strings.HasSuffix(schema, "v2") {
			record["condition"] = map[string]any{"schema": historicalCorrectionConditionSchema, "reason_code": "transient_provider_error", "text": map[string]any{"detail": "retained"}}
		}
		payload := map[string]any{RunnerInterruptionCauseField: record}
		if _, _, err := ParseRunnerInterruptionCause(payload); err == nil {
			t.Fatal("new reader accepted unproven historical schema")
		}
		if _, present, err := ParseHistoricalRunnerInterruptionCause(payload); err != nil || !present {
			t.Fatalf("historical cause present=%t err=%v", present, err)
		}
		record["unknown"] = true
		if _, _, err := ParseHistoricalRunnerInterruptionCause(payload); err == nil {
			t.Fatal("historical reader relaxed unknown-field validation")
		}
	}
}
