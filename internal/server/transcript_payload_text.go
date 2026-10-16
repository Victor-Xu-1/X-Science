package server

import "strings"

func transcriptPayloadText(payload map[string]any) string {
	for _, key := range []string{"text", "content", "detail", "message"} {
		value := webString(payload[key])
		if key == "content" && value == "" {
			value = transcriptPayloadPlainTextBlocks(payload[key])
		}
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func transcriptPayloadPlainTextBlocks(value any) string {
	blocks, ok := value.([]any)
	if !ok || len(blocks) == 0 {
		return ""
	}
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok || len(block) != 2 || block["type"] != "text" {
			return ""
		}
		if _, ok := block["text"].(string); !ok {
			return ""
		}
	}
	// Payload genesis accepts these exact plain-text blocks. Reuse the web
	// message concatenation without trimming, reordering or partial tool text.
	return webMessageText(map[string]any{"content": blocks})
}
