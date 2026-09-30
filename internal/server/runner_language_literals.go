package server

import (
	"maps"
	"regexp"
	"strings"
)

var (
	responseLanguageURLPattern                  = regexp.MustCompile(`https?://[^\s)>\]，。；！？、]+`)
	responseLanguageStructuredIdentifierPattern = regexp.MustCompile(`\b(?:[A-Za-z][A-Za-z0-9]*(?:[-_][A-Za-z0-9]+)*[-_][A-Za-z0-9]*[0-9][A-Za-z0-9_-]*|[A-Za-z][A-Za-z0-9]*_[A-Za-z0-9_-]+|[A-Z][A-Z0-9]*(?:-[A-Z0-9]+)+)\b`)
	responseLanguageUppercaseLiteralPattern     = regexp.MustCompile(`\b[A-Z0-9][A-Z0-9_-]{1,}\b`)
)

var responseLanguageLiteralPatterns = []*regexp.Regexp{
	responseLanguageFencedCodePattern,
	responseLanguageInlineCodePattern,
	responseLanguageURLPattern,
	regexp.MustCompile(`\{\{artifact:[^{}]+\}\}`),
	regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9_.-]{0,160}\.[a-z][a-z0-9]{0,11}\b`),
	responseLanguageStructuredIdentifierPattern,
	regexp.MustCompile(`\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b`),
	responseLanguageUppercaseLiteralPattern,
}

// Final-language classification uses the same literal authority as conversion.
// Pure uppercase words stay in the narrative: capitalization alone must not
// allow an ordinary English sentence to bypass the task's language contract.
func responseLanguageNarrative(text string) string {
	for _, pattern := range responseLanguageLiteralPatterns {
		text = pattern.ReplaceAllStringFunc(text, func(value string) string {
			if pattern == responseLanguageUppercaseLiteralPattern && !strings.ContainsAny(value, "0123456789_-") {
				return value
			}
			return " "
		})
	}
	return text
}

func responseLanguageProseWithoutCode(text string) string {
	text = responseLanguageFencedCodePattern.ReplaceAllString(text, " ")
	text = responseLanguageInlineCodePattern.ReplaceAllString(text, " ")
	return responseLanguageURLPattern.ReplaceAllString(text, " ")
}

func responseLanguageProtectedLiterals(text string) map[string]int {
	result := map[string]int{}
	for _, pattern := range responseLanguageLiteralPatterns {
		text = pattern.ReplaceAllStringFunc(text, func(value string) string {
			if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
				value = strings.TrimRight(value, ".,;:!?")
			}
			result[value]++
			return " "
		})
	}
	return result
}

func responseLanguageLiteralsPreserved(original, translated string) bool {
	wanted := responseLanguageProtectedLiterals(original)
	actual := responseLanguageProtectedLiterals(translated)
	return maps.Equal(wanted, actual)
}

func sessionRunnerClearlyEnglishProgress(text string) bool {
	// Ignore immutable evidence tokens before classifying short action prose.
	// Two ordinary words such as "Saving results" are a full progress update,
	// while a list of identifiers or filenames alone is not English narration.
	narrative := text
	for _, pattern := range responseLanguageLiteralPatterns {
		narrative = pattern.ReplaceAllString(narrative, " ")
	}
	han, latin, words := sessionRunnerLanguageProfile(narrative)
	// A short action followed by a filename is still narration. Preserve this
	// progress-specific signal without making final literal-only output prose.
	return han == 0 && latin >= 8 && words >= 2 || sessionRunnerNarrationIsEnglish(responseLanguageProseWithoutCode(text), true) && words > 0
}
