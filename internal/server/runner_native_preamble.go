package server

import "strings"

// A bounded, optional prefix snapshot. Public-control parsing happens upstream;
// this collector must not turn an unfinished hidden-thinking wrapper into public
// narration by merely removing its opening tag.
type runnerNativePreamble struct {
	text     strings.Builder
	overflow bool
}

func (prefix *runnerNativePreamble) append(content string) {
	if prefix.overflow {
		return
	}
	if prefix.text.Len()+len(content) > 16*1024 {
		prefix.overflow = true
		prefix.text.Reset()
		return
	}
	prefix.text.WriteString(content)
}

func (prefix *runnerNativePreamble) narration() string {
	if prefix.overflow {
		return ""
	}
	text := sessionRunnerPrivateThinkBlockPattern.ReplaceAllString(prefix.text.String(), "")
	// Early native publication is optional. Remaining markup or a partial tag
	// cannot prove a closed visible prefix; never expose its body. Complete
	// ordinary actions still pass the original tool protocol independently.
	if strings.ContainsAny(text, "<>") {
		return ""
	}
	return sessionRunnerPublicProgressNarration(text)
}
