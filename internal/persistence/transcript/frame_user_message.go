package transcript

import (
	"encoding/json"
	"strings"
)

// IsCanonicalFrameUserMessage recognizes composer provenance, including an
// ordinary message received while a task is paused. Tool/AskUser responses are
// not new task messages. Generic AppendUserEvent payloads have a separate,
// explicitly user_message contract and must not be restricted to this shape.
func IsCanonicalFrameUserMessage(eventType, clientID string, payload []byte) bool {
	var message struct {
		MessageUUID     string `json:"messageUuid"`
		UUID            string `json:"_uuid"`
		ClientMessageID string `json:"clientMessageId"`
		Role            string `json:"role"`
		Origin          string `json:"messageOrigin"`
		Text            string `json:"text"`
		Content         []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(payload, &message) != nil || message.Role != "user" ||
		message.MessageUUID == "" || message.MessageUUID != message.UUID ||
		message.ClientMessageID != clientID || clientID == "" || strings.TrimSpace(message.Text) == "" {
		return false
	}
	if !(eventType == "user_message" && message.Origin == "task_intent" ||
		eventType == "user_input_response" && message.Origin == "input_response") {
		return false
	}
	textPresent := false
	for _, block := range message.Content {
		if block.Type == "tool_result" || block.Type == "tool_use" {
			return false
		}
		textPresent = textPresent || block.Type == "text" && block.Text == message.Text
	}
	return textPresent
}
