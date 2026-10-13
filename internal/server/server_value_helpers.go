package server

import (
	"strings"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func mapValue(value any) map[string]any {
	if value == nil {
		return nil
	}
	result, _ := value.(map[string]any)
	return result
}

func compactText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 1 {
		return text[:limit]
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}
