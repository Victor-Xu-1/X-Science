package server

import (
	"strings"
	"testing"
)

func TestCompactSummaryExtractionPreservesAcceptedContent(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
	}{
		{"empty", "", ""},
		{"whitespace", " \t\r\n ", ""},
		{"legacy-plain-text", " \nLegacy plain summary.\t", "Legacy plain summary."},
		{"legacy-unicode-text", "K legacy plain summary.", "K legacy plain summary."},
		{"unrelated-markup", "<note>Legacy plain summary.</note>", "<note>Legacy plain summary.</note>"},
		{"summary-only", "<summary>PUBLIC</summary>", "PUBLIC"},
		{"summary-whitespace", "<summary> \n PUBLIC \t </summary>", "PUBLIC"},
		{"empty-summary", "<summary> \n </summary>", ""},
		{"complete-framing", "<analysis>SYNTHETIC_INTERNAL_MARKER</analysis>\n<summary>PUBLIC</summary>", "PUBLIC"},
		{"ascii-case", " \n<ANALYSIS>SYNTHETIC_INTERNAL_MARKER</ANALYSIS>\n<SuMmArY>PUBLIC</sUmMaRy>\t", "PUBLIC"},
		{"unicode-analysis-offset", "<analysis>" + strings.Repeat("K", 19) + "SYNTHETIC_INTERNAL_MARKER</analysis><summary>PUBLIC</summary>", "PUBLIC"},
		{"unicode-summary-offset", "<summary>K public summary.</summary>", "K public summary."},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := extractCompactSummaryBlock(test.input); got != test.want {
				t.Fatalf("extracted summary=%q, want=%q", got, test.want)
			}
		})
	}
}

func TestCompactSummaryExtractionRejectsAmbiguousFraming(t *testing.T) {
	for _, test := range []struct {
		name, input string
	}{
		{"analysis-only", "<analysis>SYNTHETIC_INTERNAL_MARKER</analysis>"},
		{"truncated-analysis", "<analysis>SYNTHETIC_INTERNAL_MARKER"},
		{"truncated-analysis-open", "<analysis"},
		{"truncated-analysis-close", "<analysis>SYNTHETIC_INTERNAL_MARKER</analys"},
		{"truncated-summary", "<summary>PUBLIC"},
		{"truncated-summary-open", "<summary"},
		{"truncated-summary-close", "<summary>PUBLIC</summar"},
		{"orphan-analysis-close", "SYNTHETIC_INTERNAL_MARKER</analysis>"},
		{"orphan-summary-close", "PUBLIC</summary>"},
		{"orphan-close-before-framing", "</analysis><analysis>SYNTHETIC_INTERNAL_MARKER</analysis><summary>PUBLIC</summary>"},
		{"summary-inside-analysis", "<analysis><summary>SYNTHETIC_INTERNAL_MARKER</summary></analysis>"},
		{"analysis-inside-summary", "<summary>PUBLIC<analysis>SYNTHETIC_INTERNAL_MARKER</analysis></summary>"},
		{"overlapping-blocks", "<analysis>SYNTHETIC_INTERNAL_MARKER<summary>PUBLIC</analysis></summary>"},
		{"nested-summaries", "<summary>PUBLIC_ONE<summary>PUBLIC_TWO</summary></summary>"},
		{"multiple-summaries", "<summary>PUBLIC_ONE</summary><summary>PUBLIC_TWO</summary>"},
		{"multiple-summaries-with-analysis", "<summary>PUBLIC_ONE</summary><analysis>SYNTHETIC_INTERNAL_MARKER</analysis><summary>PUBLIC_TWO</summary>"},
		{"summary-after-unclosed-analysis", "<analysis>SYNTHETIC_INTERNAL_MARKER<summary>PUBLIC</summary>"},
		{"unclosed-analysis-after-summary", "<summary>PUBLIC</summary><analysis>SYNTHETIC_INTERNAL_MARKER"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := extractCompactSummaryBlock(test.input); got != "" {
				t.Fatalf("ambiguous framed output became a summary: %q", got)
			}
		})
	}
}
