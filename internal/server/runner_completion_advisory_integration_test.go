package server

import (
	"strings"
	"testing"
)

func TestIntegratedCompletionAdvisoriesKeepEveryDistinctFinding(t *testing.T) {
	codes := []string{completionAdvisoryStructurePreview, completionAdvisoryResultQuality,
		completionAdvisoryTaskExpectations, completionAdvisoryChemicalValidation, completionAdvisoryChemicalHistory}
	const original = "Existing result: 12.5; immutable version abc-123."
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			advisories := []*sessionRunnerCompletionAdvisory{nil}
			expected := map[string]bool{}
			for _, code := range codes {
				english, chinese := sessionRunnerCompletionAdvisoryText(code)
				if english == "" || chinese == "" {
					t.Fatalf("missing bilingual notice for %s", code)
				}
				notice := english
				if language == "zh-CN" {
					notice = chinese
				}
				if expected[notice] {
					t.Fatalf("distinct advisory %s overwrote another finding", code)
				}
				expected[notice] = true
				advisories = append(advisories, &sessionRunnerCompletionAdvisory{Code: code})
			}
			result := appendSessionRunnerCompletionAdvisories(original, language, advisories...)
			if !strings.HasPrefix(result, original+"\n\n> ") {
				t.Fatal("advisories replaced the original result")
			}
			for notice := range expected {
				if strings.Count(result, notice) != 1 {
					t.Fatalf("notice missing or duplicated: %s", notice)
				}
			}
			if appendSessionRunnerCompletionAdvisories(result, language, advisories...) != result {
				t.Fatal("repeated presentation duplicated a finding")
			}
			if appendSessionRunnerCompletionAdvisories("", language, advisories...) != "" {
				t.Fatal("advisories manufactured a missing primary answer")
			}
		})
	}
}
