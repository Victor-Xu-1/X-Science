package agentruntime

import "strings"

// TextToolEnvelopePresentationError reports an unexecuted text proposal. It
// never supplies tool arguments or grants authority to interpret the text.
type TextToolEnvelopePresentationError struct{}

func (*TextToolEnvelopePresentationError) Error() string {
	return "model returned a text tool envelope instead of a native tool call"
}

const maxTextToolEnvelopeProbeBytes = 256

type modelTextToolEnvelopeProbe struct {
	names   map[string]bool
	pending string
	plain   bool
	emit    func(string) error
}

func newModelTextToolEnvelopeProbe(tools []ToolSchema, emit func(string) error) *modelTextToolEnvelopeProbe {
	names := make(map[string]bool)
	for _, tool := range tools {
		if tool.Name != "" && tool.EffectiveExposure() == ToolExposureDirect {
			names[tool.Name] = true
		}
	}
	return &modelTextToolEnvelopeProbe{names: names, plain: len(names) == 0, emit: emit}
}

func (probe *modelTextToolEnvelopeProbe) write(content string) error {
	if probe.plain {
		return probe.emit(content)
	}
	probe.pending += content
	return probe.drain(false)
}

func (probe *modelTextToolEnvelopeProbe) finish() error { return probe.drain(true) }

func (probe *modelTextToolEnvelopeProbe) drain(final bool) error {
	if probe.plain || probe.pending == "" {
		return nil
	}
	decision := textToolEnvelopePrefix(probe.pending, probe.names, final)
	if decision < 0 {
		probe.pending = ""
		return &TextToolEnvelopePresentationError{}
	}
	if decision == 0 && len(probe.pending) > maxTextToolEnvelopeProbeBytes {
		if strings.TrimSpace(probe.pending) == "" {
			content := probe.pending
			probe.pending = ""
			return probe.emit(content)
		}
		probe.pending = ""
		return &TextToolEnvelopePresentationError{}
	}
	if decision == 0 && !final && len(probe.pending) <= maxTextToolEnvelopeProbeBytes {
		return nil
	}
	probe.plain = true
	content := probe.pending
	probe.pending = ""
	return probe.emit(content)
}

// Inspect only the opening shape and advertised name, never XML parameters.
// 0 means a possible fragmented prefix, 1 ordinary text, -1 an invalid proposal.
func textToolEnvelopePrefix(content string, names map[string]bool, final bool) int {
	if len(names) == 0 {
		return 1
	}
	trim := func(value string) string { return strings.TrimLeft(value, " \t\r\n") }
	value := trim(content)
	const wrapper = "<tool_call>"
	const function = "<function="
	if len(value) < len(wrapper) && strings.HasPrefix(wrapper, value) {
		return 0
	}
	if !strings.HasPrefix(value, wrapper) {
		return 1
	}
	value = trim(value[len(wrapper):])
	if len(value) < len(function) && strings.HasPrefix(function, value) {
		return 0
	}
	if !strings.HasPrefix(value, function) {
		return 1
	}
	value = trim(value[len(function):])
	if final && value == "" {
		return -1
	}
	if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
		value = value[1:]
	}
	end := strings.IndexAny(value, " \t\r\n>'\"")
	if end >= 0 {
		if names[value[:end]] {
			return -1
		}
		return 1
	}
	for name := range names {
		if strings.HasPrefix(name, value) {
			if final && (name == value || len(value) >= 3) {
				return -1
			}
			return 0
		}
	}
	return 1
}
