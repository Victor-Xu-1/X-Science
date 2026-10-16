package server

import (
	"strings"
	"unicode/utf8"

	"x-science/internal/agentruntime"
)

// A short tail can be either an intentional repetition or a failed resumption.
// Delay only the ambiguous prefix until the provider boundary disambiguates it.
// Successful completion and tool boundaries preserve it verbatim. This is not
// a general text deduplicator and never edits already published history.
type continuationTailProbe struct {
	window               string
	pending              strings.Builder
	released             bool
	toolBoundary         bool
	nativePreamble       *agentruntime.ModelStreamEvent
	nativePreambleOffset int
	emit                 func(agentruntime.ModelStreamEvent) error
}

func newContinuationTailProbe(prefix string, emit func(agentruntime.ModelStreamEvent) error) *continuationTailProbe {
	const windowBytes = 4096
	start := max(0, len(prefix)-windowBytes)
	for start < len(prefix) && !utf8.RuneStart(prefix[start]) {
		start++
	}
	return &continuationTailProbe{window: prefix[start:], emit: emit}
}

func (p *continuationTailProbe) event(event agentruntime.ModelStreamEvent) error {
	if event.Kind == agentruntime.ModelStreamEventPrivateReasoning || (event.Kind == "" && event.ReasoningActive) {
		return p.send(event)
	}
	if event.Kind == agentruntime.ModelStreamEventToolCallBoundary && !p.released {
		// A native tool-start delta is only a proposal. An output limit may
		// still truncate its arguments; wait for the validated terminal result.
		p.toolBoundary = true
		return nil
	}
	if event.Kind == agentruntime.ModelStreamEventNativePreambleBoundary && !p.released {
		// A provider-bound prefix does not disambiguate an exact replayed tail.
		// Hold it with that tail until the original continuation authority resolves
		// the response; it must not make a truncated replay appear to be progress.
		copy := event
		p.nativePreamble = &copy
		p.nativePreambleOffset = p.pending.Len()
		return nil
	}
	if event.Kind != agentruntime.ModelStreamEventContentDelta && event.Kind != "" {
		if err := p.flush(); err != nil {
			return err
		}
		p.released = true
		return p.send(event)
	}
	if p.released {
		return p.send(event)
	}
	p.pending.WriteString(event.ContentDelta)
	if p.pending.Len() > len(p.window) || !strings.Contains(p.window, p.pending.String()) {
		return p.flush()
	}
	return nil
}

func (p *continuationTailProbe) onlyReplayedTail(filteredPrefix bool) bool {
	return !p.released && ((p.pending.Len() > 0 && strings.HasSuffix(p.window, p.pending.String())) ||
		(p.pending.Len() == 0 && filteredPrefix))
}

func (p *continuationTailProbe) flush() error {
	text := p.pending.String()
	p.pending.Reset()
	prefixEnd := len(text)
	if p.nativePreamble != nil {
		// Later content may disambiguate the pending historical tail, but it
		// cannot become part of the earlier provider-bound prefix snapshot.
		prefixEnd = p.nativePreambleOffset
	}
	if prefixEnd > 0 {
		p.released = true
		if err := p.send(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: text[:prefixEnd]}); err != nil {
			return err
		}
	}
	if p.toolBoundary {
		p.toolBoundary = false
		p.released = true
		if err := p.send(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventToolCallBoundary}); err != nil {
			return err
		}
	}
	if p.nativePreamble != nil {
		event := *p.nativePreamble
		p.nativePreamble = nil
		p.nativePreambleOffset = 0
		if err := p.send(event); err != nil {
			return err
		}
	}
	if prefixEnd < len(text) {
		p.released = true
		return p.send(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: text[prefixEnd:]})
	}
	return nil
}

func (p *continuationTailProbe) send(event agentruntime.ModelStreamEvent) error {
	if p.emit == nil {
		return nil
	}
	return p.emit(event)
}
