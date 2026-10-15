package server

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestCompletionQualityIsAdvisoryWithoutDisablingVerification(t *testing.T) {
	quality := []string{
		"numeric_table_mismatch:report.md<->results.csv key=A",
		"numeric_transposed_table_mismatch:report.md<->results.csv entity=A",
		"table_row_identity_mismatch:report.md<->results.csv key_column=id",
		"artifact_table_contract_value_mismatch:report.md key=A",
		"artifact_table_contract_row_missing:report.md key=A",
		"artifact_table_contract_row_unexpected:report.md key=A",
		"evidence_record_depth_required:sources.csv source_type=publication",
		"evidence_record_class_missing:source_type=publication",
		"source_claim_missing_attested_excerpt:source_evidence.json source=s1",
		"machine_validation_missing_passing_check:validation.json",
		"machine_validation_false_check:validation.json path=checks.passes",
		"machine_validation_failed_status:validation.json path=status value=failed",
		"machine_validation_reported_failure:validation.json path=failures",
		"machine_validation_invalid_root:validation.json",
		"rank_score_pair_missing:results.csv rank=rank score=score",
		"rank_score_non_numeric:results.csv key=A columns=score/rank",
		"environment_claim_conflict:package in report.md",
	}
	hard := []string{
		"missing_artifact_reference:missing.csv in report.md",
		"machine_validation_invalid_json:validation.json",
		"machine_validation_trailing_data:validation.json",
		"authoritative_artifact_reference_conflict:report.md",
		"unrecognized_integrity_failure:report.md",
	}
	for _, disabled := range []bool{false, true} {
		name := "default"
		if disabled {
			name = "explicit-review-off"
		}
		t.Run(name, func(t *testing.T) {
			failures := append(append([]string(nil), quality...), hard...)
			before := append([]string(nil), failures...)
			run := &sessionRunnerChatRun{VerificationExplicitlyDisabled: disabled}
			if got := sessionRunnerFatalCrossArtifactFailures(run, failures); !reflect.DeepEqual(got, hard) {
				t.Fatalf("quality findings still prevent delivery or integrity checks were lost: got=%#v want=%#v", got, hard)
			}
			if !reflect.DeepEqual(failures, before) {
				t.Fatal("advisory classification rewrote the original findings")
			}
		})
	}
	if got := sessionRunnerFatalCrossArtifactFailures(nil, quality); !reflect.DeepEqual(got, quality) {
		t.Fatalf("missing run authority silently relaxed validation: %#v", got)
	}
}

func TestCompletionQualityPolicyPreservesReportedFailedChecksWithoutBlockingDelivery(t *testing.T) {
	input := `{"status":"failed","checks":{"valid":false},"failures":["quality threshold not met"]}`
	findings, err := scanRunnerMachineValidation(context.Background(), strings.NewReader(input), "validation.json")
	if err != nil || len(findings) == 0 {
		t.Fatalf("actual report scanner lost the failed quality findings: %#v, %v", findings, err)
	}
	before := append([]string(nil), findings...)
	if fatal := sessionRunnerFatalCrossArtifactFailures(&sessionRunnerChatRun{}, findings); len(fatal) != 0 {
		t.Fatalf("a report's own failed checks were substituted for actual execution status: %#v", fatal)
	}
	if !reflect.DeepEqual(before, findings) {
		t.Fatal("failed report checks were relabelled or removed")
	}
	for _, empty := range []string{`{}`, `[]`} {
		findings, err := scanRunnerMachineValidation(context.Background(), strings.NewReader(empty), "validation.json")
		if err != nil || len(findings) == 0 {
			t.Fatalf("empty report finding unavailable: %#v, %v", findings, err)
		}
		if fatal := sessionRunnerFatalCrossArtifactFailures(&sessionRunnerChatRun{}, findings); len(fatal) != 0 {
			t.Fatalf("valid empty JSON was mistaken for corrupt bytes: %#v", fatal)
		}
	}
	broken, err := scanRunnerMachineValidation(context.Background(), strings.NewReader(`{"status":`), "validation.json")
	if err != nil || len(broken) != 1 || !strings.HasPrefix(broken[0], "machine_validation_invalid_json:") {
		t.Fatalf("broken artifact bytes were not identified: %#v, %v", broken, err)
	}
	if fatal := sessionRunnerFatalCrossArtifactFailures(&sessionRunnerChatRun{}, broken); !reflect.DeepEqual(fatal, broken) {
		t.Fatalf("corrupt artifact became a quality-only notice: %#v", fatal)
	}
}
