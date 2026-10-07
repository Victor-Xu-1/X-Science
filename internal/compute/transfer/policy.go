// Package transfer owns the data selection and integrity contracts shared by
// local staging, SSH and provider-backed job delivery. It cannot start work or
// grant access to a path; callers retain their task/provider authority.
package transfer

import (
	"errors"
	"math"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Output struct {
	Glob       string `json:"glob"`
	Visibility string `json:"visibility,omitempty"`
}

type Policy struct {
	RequiredPaths  *PathIndex `json:"-"`
	RequiredSHA256 string     `json:"required_sha256,omitempty"`
	Outputs        []Output   `json:"outputs,omitempty"`
	Exclude        []string   `json:"exclude,omitempty"`
	MaxFileBytes   int64      `json:"max_file_bytes,omitempty"`
	MaxTotalBytes  int64      `json:"max_total_bytes,omitempty"`
}

type pattern struct {
	fullPath bool
	expr     *regexp.Regexp
}

type Selector struct {
	policy  Policy
	include []pattern
	exclude []pattern
}

func (policy Policy) Validate() error {
	_, err := policy.Compile()
	return err
}

// Compile freezes and compiles control fields once, not once per data file.
// Zero limits mean unspecified; actual storage admission is a separate fact.
func (policy Policy) Compile() (*Selector, error) {
	if policy.MaxFileBytes < 0 || policy.MaxTotalBytes < 0 ||
		(policy.MaxFileBytes > 0 && policy.MaxTotalBytes > 0 && policy.MaxFileBytes > policy.MaxTotalBytes) {
		return nil, errors.New("transfer byte budgets are invalid")
	}
	selector := &Selector{policy: Policy{MaxFileBytes: policy.MaxFileBytes, MaxTotalBytes: policy.MaxTotalBytes}}
	for _, output := range policy.Outputs {
		if output.Visibility != "" && output.Visibility != "featured" && output.Visibility != "hidden" {
			return nil, errors.New("output visibility is invalid")
		}
		compiled, err := compilePattern(output.Glob)
		if err != nil {
			return nil, err
		}
		selector.include = append(selector.include, compiled)
	}
	for _, excluded := range policy.Exclude {
		compiled, err := compilePattern(excluded)
		if err != nil {
			return nil, err
		}
		selector.exclude = append(selector.exclude, compiled)
	}
	return selector, nil
}

func (policy Policy) Select(name string, size, used int64) (bool, string) {
	selector, err := policy.Compile()
	if err != nil {
		return false, "invalid_policy"
	}
	return selector.Select(name, size, used)
}

func (selector *Selector) Select(name string, size, used int64) (bool, string) {
	return selector.selectPath(name, size, used, false)
}

func (selector *Selector) selectPath(name string, size, used int64, required bool) (bool, string) {
	if selector == nil || size < 0 || used < 0 {
		return false, "invalid_size"
	}
	isLog := name == "stdout.log" || name == "stderr.log"
	if !isLog {
		if !strings.HasPrefix(name, "out/") {
			return false, "excluded"
		}
		for _, excluded := range selector.exclude {
			if excluded.matches(name) {
				return false, "excluded"
			}
		}
		if len(selector.include) > 0 && !required {
			matched := false
			for _, included := range selector.include {
				if included.matches(name) {
					matched = true
					break
				}
			}
			if !matched {
				return false, "not_selected"
			}
		}
	}
	if selector.policy.MaxFileBytes > 0 && size > selector.policy.MaxFileBytes {
		return false, "max_file_bytes"
	}
	if limit := selector.policy.MaxTotalBytes; limit > 0 && (used > limit || size > limit-used) {
		return false, "max_total_bytes"
	}
	if used > math.MaxInt64-size {
		return false, "invalid_size"
	}
	return true, ""
}

func GlobMatches(value, candidate string) bool {
	compiled, err := compilePattern(value)
	return err == nil && compiled.matches(candidate)
}

func (compiled pattern) matches(candidate string) bool {
	candidate = strings.TrimPrefix(candidate, "./")
	if !compiled.fullPath {
		candidate = strings.TrimPrefix(candidate, "out/")
	}
	return compiled.expr.MatchString(candidate)
}

func compilePattern(value string) (pattern, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return pattern{}, errors.New("output glob is invalid")
	}
	value = path.Clean(value)
	if value == "." || value == ".." || strings.HasPrefix(value, "../") || path.IsAbs(value) {
		return pattern{}, errors.New("output glob escapes the job output root")
	}
	var expression strings.Builder
	expression.WriteString("^")
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '*':
			if index+1 < len(value) && value[index+1] == '*' {
				index++
				if index+1 < len(value) && value[index+1] == '/' {
					index++
					expression.WriteString("(?:(?s:.*)/)?")
				} else {
					expression.WriteString("(?s:.*)")
				}
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(regexp.QuoteMeta(string(value[index])))
		}
	}
	expression.WriteString("$")
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return pattern{}, err
	}
	return pattern{fullPath: strings.HasPrefix(value, "out/"), expr: compiled}, nil
}
