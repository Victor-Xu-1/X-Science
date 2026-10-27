package literaturediscovery

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var doiPattern = regexp.MustCompile(`^10\.[0-9]{4,9}/[^\s]+$`)

// NormalizeDOI is the single identity function for Library discovery. Balanced
// suffix parentheses are legitimate DOI characters, not citation punctuation.
func NormalizeDOI(raw string) (string, bool) {
	if !utf8.ValidString(raw) || len(raw) > 8192 {
		return "", false
	}
	value := strings.TrimSpace(raw)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "doi:") {
		value = strings.TrimSpace(value[4:])
		lower = strings.ToLower(value)
	}
	for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "https://dx.doi.org/", "http://dx.doi.org/"} {
		if strings.HasPrefix(lower, prefix) {
			value = value[len(prefix):]
			break
		}
	}
	balances := map[byte]int{')': 0, ']': 0, '}': 0}
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '(':
			balances[')']--
		case '[':
			balances[']']--
		case '{':
			balances['}']--
		case ')', ']', '}':
			balances[value[index]]++
		}
	}
	for len(value) > 0 {
		last := value[len(value)-1]
		if strings.ContainsRune(".,;:", rune(last)) {
			value = value[:len(value)-1]
		} else if count, bracket := balances[last]; bracket && count > 0 {
			balances[last]--
			value = value[:len(value)-1]
		} else {
			break
		}
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 2048 || !doiPattern.MatchString(value) {
		return "", false
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return "", false
		}
	}
	return value, true
}
