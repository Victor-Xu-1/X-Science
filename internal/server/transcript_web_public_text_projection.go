package server

import (
	"strings"
	"unicode"
)

// Kept intervals refer to the original UTF8 bytes, in source order. Deletion
// composes these intervals rather than matching output text back to a source.
type sessionRunnerPublicTextInterval struct{ start, end int }

type sessionRunnerPublicTextProjection struct {
	text string
	kept []sessionRunnerPublicTextInterval
}

func sessionRunnerPublicTextIdentity(text string) sessionRunnerPublicTextProjection {
	projection := sessionRunnerPublicTextProjection{text: text}
	if text != "" {
		projection.kept = []sessionRunnerPublicTextInterval{{0, len(text)}}
	}
	return projection
}

// retain selects ordered, non-overlapping intervals in the current text and
// preserves their original source coordinates through previous deletions.
func (projection sessionRunnerPublicTextProjection) retain(intervals []sessionRunnerPublicTextInterval) sessionRunnerPublicTextProjection {
	var text strings.Builder
	result := sessionRunnerPublicTextProjection{}
	segment, offset := 0, 0
	for _, interval := range intervals {
		if interval.start == interval.end {
			continue
		}
		text.WriteString(projection.text[interval.start:interval.end])
		for segment < len(projection.kept) && offset+projection.kept[segment].end-projection.kept[segment].start <= interval.start {
			offset += projection.kept[segment].end - projection.kept[segment].start
			segment++
		}
		for index, position := segment, offset; index < len(projection.kept) && position < interval.end; index++ {
			source := projection.kept[index]
			end := position + source.end - source.start
			start, stop := max(position, interval.start), min(end, interval.end)
			if start < stop {
				kept := sessionRunnerPublicTextInterval{source.start + start - position, source.start + stop - position}
				if count := len(result.kept); count > 0 && result.kept[count-1].end == kept.start {
					result.kept[count-1].end = kept.end
				} else {
					result.kept = append(result.kept, kept)
				}
			}
			position = end
		}
	}
	result.text = text.String()
	return result
}

// This is the shared private-only parser. Model/publication consumers retain
// their existing signature and output; historical line cleanup stays separate.
func sessionRunnerPrivateThinkProjection(content string) sessionRunnerPublicTextProjection {
	kept := []sessionRunnerPublicTextInterval{}
	depth, after := 0, 0
	for _, location := range sessionRunnerPrivateThinkTagPattern.FindAllStringIndex(content, -1) {
		if depth == 0 && after < location[0] {
			kept = append(kept, sessionRunnerPublicTextInterval{after, location[0]})
		}
		tag := strings.TrimSpace(content[location[0]+1 : location[1]-1])
		if strings.HasPrefix(tag, "/") {
			depth = max(0, depth-1)
		} else {
			depth++
		}
		after = location[1]
	}
	if depth == 0 && after < len(content) {
		kept = append(kept, sessionRunnerPublicTextInterval{after, len(content)})
	}
	projection := sessionRunnerPublicTextIdentity(content).retain(kept)
	if partial := sessionRunnerPartialThinkTagPattern.FindStringIndex(projection.text); partial != nil {
		projection = projection.retain([]sessionRunnerPublicTextInterval{{0, partial[0]}})
	}
	return projection
}

// Diagnostic references remain immutable in the canonical answer and model
// replay. Consume only complete standalone values through the existing codec;
// skip unknown/business JSON as a unit instead of searching inside it.
func (projection sessionRunnerPublicTextProjection) withoutCompletionObservationReferences() sessionRunnerPublicTextProjection {
	content := projection.text
	kept := []sessionRunnerPublicTextInterval{}
	after, cursor := 0, 0
	lineEnd := func(start int) int {
		if n := strings.IndexByte(content[start:], '\n'); n >= 0 {
			return start + n + 1
		}
		return len(content)
	}
	remove := func(start, end int) {
		if after < start {
			kept = append(kept, sessionRunnerPublicTextInterval{after, start})
		}
		after = end
	}
	for cursor < len(content) {
		end := lineEnd(cursor)
		line := strings.TrimSpace(content[cursor:end])
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			marker, count := line[0], 0
			for count < len(line) && line[count] == marker {
				count++
			}
			info := strings.TrimSpace(line[count:])
			closeStart, closeEnd := end, end
			for closeStart < len(content) {
				closeEnd = lineEnd(closeStart)
				closing := strings.TrimSpace(content[closeStart:closeEnd])
				if len(closing) >= count && strings.Trim(closing, string(marker)) == "" {
					break
				}
				closeStart = closeEnd
			}
			if closeStart == len(content) {
				break
			}
			if info == "" || strings.EqualFold(info, "json") {
				body := content[end:closeStart]
				n, known := sessionRunnerCompletionObservationReference(body)
				if known && strings.TrimSpace(body[n:]) == "" {
					remove(cursor, closeEnd)
				}
			}
			cursor = closeEnd
			continue
		}
		if strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "\"") {
			start := cursor + len(content[cursor:end]) - len(strings.TrimLeftFunc(content[cursor:end], unicode.IsSpace))
			n, known := sessionRunnerCompletionObservationReference(content[start:])
			// A truncated outer value must not expose an inner object as a
			// standalone reference. Retain its unparsed remainder unchanged.
			if n == 0 {
				break
			}
			valueEnd := start + n
			next := lineEnd(valueEnd)
			if known && strings.TrimSpace(content[valueEnd:next]) == "" {
				remove(cursor, next)
			}
			cursor = next
			continue
		}
		cursor = end
	}
	if after == 0 {
		return projection
	}
	if after < len(content) {
		kept = append(kept, sessionRunnerPublicTextInterval{after, len(content)})
	}
	return projection.retain(kept)
}

// Live and historical presentation share deletion coordinates and the host
// codec. Only a valid canonical envelope supplies Items/Sources, never quotes.
func projectTranscriptWebCompletionObservationReferences(streamPayload, payload map[string]any, attempt int64) {
	original := transcriptPayloadText(payload)
	projection := sessionRunnerCanonicalPublicTextProjection(original)
	streamPayload["data"] = projection.text
	wire := sessionRunnerCompletionAdvisoriesFromPayload(payload, attempt)
	if wire == nil {
		return
	}
	spans, valid := projection.advisorySpans(original, wire.Spans)
	if !valid {
		return
	}
	projected := *wire
	projected.Spans = spans
	projected.TextSHA256 = completionAdvisorySHA(projection.text)
	projected.TextLength = sessionRunnerPublicTextUTF16Length(projection.text)
	if verified := sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": projection.text, "completion_advisories": projected}, attempt); verified != nil {
		streamPayload["completion_advisories"] = verified
	}
}

func sessionRunnerHistoricalPublicTextProjection(content string, preservedCodes map[string]struct{}) sessionRunnerPublicTextProjection {
	projection := sessionRunnerCanonicalPublicTextProjection(content)
	if !sessionRunnerPublicTextExposesUntrustedInternals(projection.text, preservedCodes) {
		return projection
	}
	kept := []sessionRunnerPublicTextInterval{}
	after, hasLine := 0, false
	for _, line := range strings.Split(projection.text, "\n") {
		if strings.TrimSpace(line) != "" && !sessionRunnerPublicTextExposesUntrustedInternals(line, preservedCodes) {
			// Join using this line's actual preceding LF. In particular a
			// host-owned separator remains owned after blank-line removal.
			if hasLine {
				kept = append(kept, sessionRunnerPublicTextInterval{after - 1, after})
			}
			kept = append(kept, sessionRunnerPublicTextInterval{after, after + len(line)})
			hasLine = true
		}
		after += len(line) + 1
	}
	projection = projection.retain(kept)
	left := strings.TrimLeftFunc(projection.text, unicode.IsSpace)
	start := len(projection.text) - len(left)
	end := start + len(strings.TrimRightFunc(left, unicode.IsSpace))
	return projection.retain([]sessionRunnerPublicTextInterval{{start, end}})
}

func sessionRunnerPublicTextUTF16Length(text string) int {
	length := 0
	for _, character := range text {
		length++
		if character > 0xffff {
			length++
		}
	}
	return length
}

func sessionRunnerPublicTextByteBoundary(text string, units int) (int, bool) {
	after := 0
	for position, character := range text {
		if after == units {
			return position, true
		}
		after++
		if character > 0xffff {
			after++
		}
		if after > units {
			return 0, false
		}
	}
	return len(text), after == units
}

// Only previously validated host ranges enter this mapping. Identical model
// quotations are irrelevant: ownership follows retained source bytes alone.
func (projection sessionRunnerPublicTextProjection) advisorySpans(original string, spans []completionAdvisorySpan) ([]completionAdvisorySpan, bool) {
	result := make([]completionAdvisorySpan, 0, len(spans))
	for _, span := range spans {
		start, validStart := sessionRunnerPublicTextByteBoundary(original, span.Start)
		end, validEnd := sessionRunnerPublicTextByteBoundary(original, span.End)
		if !validStart || !validEnd {
			return nil, false
		}
		first, last, position := -1, -1, 0
		for _, kept := range projection.kept {
			left, right := max(start, kept.start), min(end, kept.end)
			if left < right {
				if first < 0 {
					first = position + left - kept.start
				}
				last = position + right - kept.start
			}
			position += kept.end - kept.start
		}
		if first >= 0 {
			result = append(result, completionAdvisorySpan{
				Start: sessionRunnerPublicTextUTF16Length(projection.text[:first]),
				End:   sessionRunnerPublicTextUTF16Length(projection.text[:last]),
				Text:  projection.text[first:last],
			})
		}
	}
	return result, true
}
