package server

import "strings"

func extractCompactSummaryBlock(output string) string {
	text := strings.TrimSpace(output)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	start := strings.Index(lower, "<summary>")
	end := strings.LastIndex(lower, "</summary>")
	if start >= 0 && end > start {
		start += len("<summary>")
		return strings.TrimSpace(text[start:end])
	}
	return strings.TrimSpace(removeCompactTagBlock(text, "analysis"))
}

func removeCompactTagBlock(text string, tag string) string {
	lower := strings.ToLower(text)
	open := "<" + strings.ToLower(tag) + ">"
	close := "</" + strings.ToLower(tag) + ">"
	for {
		start := strings.Index(lower, open)
		end := strings.Index(lower, close)
		if start < 0 || end < start {
			return text
		}
		end += len(close)
		text = text[:start] + text[end:]
		lower = strings.ToLower(text)
	}
}
