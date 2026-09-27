package server

import (
	"strings"
	"unicode/utf8"

	"synon-go/internal/agentruntime"
)

// A short tail can be either an intentional repetition or a failed resumption.
// Delay only the ambiguous prefix until the provider boundary disambiguates it.
// Successful completion and tool boundaries preserve it verbatim. This is not
// a general text deduplicator and never edits already published history.
type continuationTailProbe struct {
	window       string
	pending      strings.Builder
	released     bool
	toolBoundary bool
	emit         func(agentruntime.ModelStreamEvent) error
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
	if p.pending.Len() > 0 {
		text := p.pending.String()
		p.pending.Reset()
		p.released = true
		if err := p.send(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: text}); err != nil {
			return err
		}
	}
	if p.toolBoundary {
		p.toolBoundary = false
		p.released = true
		return p.send(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventToolCallBoundary})
	}
	return nil
}

func (p *continuationTailProbe) send(event agentruntime.ModelStreamEvent) error {
	if p.emit == nil {
		return nil
	}
	return p.emit(event)
}
