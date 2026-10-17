package server

import "strings"

type compactSummaryTag uint8

const (
	compactSummaryNoTag compactSummaryTag = iota
	compactSummaryAnalysisTag
	compactSummarySummaryTag
	compactSummaryMalformedTag
)

func extractCompactSummaryBlock(output string) string {
	text := strings.TrimSpace(output)
	openTag := compactSummaryNoTag
	sawFraming := false
	summaryStart, summaryEnd := -1, -1
	// Keep offsets in the original buffer; case folding must not change them.
	for offset := 0; offset < len(text); {
		if text[offset] != '<' {
			offset++
			continue
		}
		tag, closing, width := scanCompactSummaryTag(text[offset:])
		if tag == compactSummaryNoTag {
			offset++
			continue
		}
		if tag == compactSummaryMalformedTag {
			return ""
		}
		sawFraming = true
		if closing {
			if openTag != tag {
				return ""
			}
			openTag = compactSummaryNoTag
			if tag == compactSummarySummaryTag {
				summaryEnd = offset
			}
		} else {
			if openTag != compactSummaryNoTag {
				return ""
			}
			if tag == compactSummarySummaryTag {
				if summaryStart >= 0 {
					return ""
				}
				summaryStart = offset + width
			}
			openTag = tag
		}
		offset += width
	}
	if openTag != compactSummaryNoTag || (sawFraming && summaryEnd < 0) {
		return ""
	}
	if summaryEnd >= 0 {
		return strings.TrimSpace(text[summaryStart:summaryEnd])
	}
	return text
}

// scanCompactSummaryTag recognizes only ASCII reserved names. A truncated name
// or malformed reserved tag is framing, not a legacy plain-text response.
func scanCompactSummaryTag(text string) (compactSummaryTag, bool, int) {
	nameStart := 1 // The caller has already found '<'.
	closing := nameStart < len(text) && text[nameStart] == '/'
	if closing {
		nameStart++
	}
	for _, candidate := range [...]struct {
		name string
		tag  compactSummaryTag
	}{
		{"analysis", compactSummaryAnalysisTag},
		{"summary", compactSummarySummaryTag},
	} {
		matched := 0
		for matched < len(candidate.name) && nameStart+matched < len(text) {
			b := text[nameStart+matched]
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if b != candidate.name[matched] {
				break
			}
			matched++
		}
		if matched == 0 {
			continue
		}
		nameEnd := nameStart + matched
		if nameEnd == len(text) {
			return compactSummaryMalformedTag, false, 0
		}
		if matched != len(candidate.name) {
			continue
		}
		if text[nameEnd] == '>' {
			return candidate.tag, closing, nameEnd + 1
		}
		// Extended names are unrelated markup, not reserved framing.
		b := text[nameEnd]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '-' || b == '_' || b == ':' || b == '.' || b >= 0x80 {
			continue
		}
		return compactSummaryMalformedTag, false, 0
	}
	return compactSummaryNoTag, false, 0
}
