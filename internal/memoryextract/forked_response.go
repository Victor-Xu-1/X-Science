package memoryextract

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrInvalidResponse    = errors.New("memory extraction response is invalid")
	forkedJSONFencePrefix = regexp.MustCompile(`(?i)^\s*` + "```" + `(?:json)?\s*\n?`)
	forkedJSONFenceSuffix = regexp.MustCompile(`\n?` + "```" + `\s*$`)
)

// ParseForkedResponse retains first-balanced-object and wrapper compatibility.
// A malformed/missing object is not an explicit empty acknowledgement: callers
// must preserve the uncommitted source for their existing bounded retry path.
func ParseForkedResponse(value string) (map[string]any, error) {
	value = forkedJSONFencePrefix.ReplaceAllString(value, "")
	value = forkedJSONFenceSuffix.ReplaceAllString(value, "")
	start := strings.IndexByte(value, '{')
	if start < 0 {
		return nil, fmt.Errorf("%w: missing JSON object", ErrInvalidResponse)
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(value); index++ {
		current := value[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case current == '\\':
				escaped = true
			case current == '"':
				inString = false
			}
			continue
		}
		switch current {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				var decoded map[string]any
				if err := json.Unmarshal([]byte(value[start:index+1]), &decoded); err != nil || decoded == nil {
					return nil, fmt.Errorf("%w: malformed JSON object", ErrInvalidResponse)
				}
				return decoded, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: incomplete JSON object", ErrInvalidResponse)
}
