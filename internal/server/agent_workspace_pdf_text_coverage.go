package server

import (
	"io"
	"unicode"
	"unicode/utf8"
)

// The extractor goroutine owns these observations until its finished channel
// closes. Selected-page lists are bounded by the existing read_file contract.
// Page zero means a whole-document extraction and never invents page coverage.
type agentWorkspacePDFTextCoverage struct {
	hasText     bool
	textPages   []int
	noTextPages []int
}

func (coverage *agentWorkspacePDFTextCoverage) record(page int, hasText bool) {
	coverage.hasText = coverage.hasText || hasText
	if page == 0 {
		return
	}
	if hasText {
		coverage.textPages = append(coverage.textPages, page)
	} else {
		coverage.noTextPages = append(coverage.noTextPages, page)
	}
}

// Observe the actual successfully forwarded stdout without retaining its body
// or changing stream bytes/line offsets. A split UTF-8 whitespace rune is not
// text; only at most three pending bytes survive between writes.
type agentWorkspacePDFBodyWriter struct {
	destination io.Writer
	hasText     bool
	pending     []byte
}

func (writer *agentWorkspacePDFBodyWriter) Write(raw []byte) (int, error) {
	count, err := writer.destination.Write(raw)
	if !writer.hasText {
		writer.observe(raw[:count])
	}
	return count, err
}

func (writer *agentWorkspacePDFBodyWriter) observe(raw []byte) {
	if len(writer.pending) > 0 {
		for len(raw) > 0 && !utf8.FullRune(writer.pending) {
			writer.pending = append(writer.pending, raw[0])
			raw = raw[1:]
		}
		if !utf8.FullRune(writer.pending) {
			return
		}
		value, _ := utf8.DecodeRune(writer.pending)
		writer.pending = writer.pending[:0]
		if !unicode.IsSpace(value) {
			writer.hasText = true
			return
		}
	}
	for len(raw) > 0 {
		if !utf8.FullRune(raw) {
			writer.pending = append(writer.pending[:0], raw...)
			return
		}
		value, size := utf8.DecodeRune(raw)
		if !unicode.IsSpace(value) {
			writer.hasText = true
			return
		}
		raw = raw[size:]
	}
}
