package server

import (
	"fmt"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Keep the terminal notice outside the assistant segment that holds the reply.
// The event ID makes the same notice idempotent in live delivery and history.
func transcriptWebRetainedResponseNoticeID(assistantID string, eventID int64) string {
	return fmt.Sprintf("%s-cleanup-%d", assistantID, eventID)
}

func transcriptWebRetainedResponseNotice(
	projection transcriptstore.TerminalProjection,
	assistantID string,
) map[string]any {
	id := transcriptWebRetainedResponseNoticeID(assistantID, projection.EventID)
	status, tipType := "error", "error"
	if projection.Superseded {
		status, tipType = "finish", "warning"
	}
	notice := map[string]any{
		"id": id, "msg_id": id, "conversation_id": projection.SessionID,
		"type": "tips", "position": "center", "status": status,
		"terminal_status": projection.TerminalStatus, "terminal_reason_code": projection.ReasonCode,
		"created_at": projection.CreatedAt.UnixMilli(),
		"content":    map[string]any{"content": projection.Detail, "type": tipType},
	}
	if projection.Superseded {
		notice["terminal_superseded"] = true
	}
	return notice
}

func settleTranscriptWebRetainedResponse(message map[string]any, assistantID string) error {
	content, ok := message["content"].(map[string]any)
	if !ok || assistantID == "" || message["id"] != assistantID || message["type"] != "text" ||
		webString(content["content"]) == "" {
		return transcriptstore.ErrEventConflict
	}
	return transcriptWebSettleAssistantMessage(message, "finish")
}
