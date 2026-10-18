package server

import (
	"strings"
	"unicode/utf8"
)

const (
	conversationSkillTranscriptMaxBytes = 256 << 10
	conversationSkillMessageMaxBytes    = 12 << 10
)

func truncateConversationSkillText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxBytes {
		return value
	}
	cut := value[:maxBytes]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut) + "\n[truncated]"
}
