package workspace

import (
	"context"
	"encoding/json"
)

// Presentation commands share one narrow write, without advancing activity or
// replacing runtime-owned context, draft, history, artifacts or pin fields.
func storeConversationPresentationExtraTx(ctx context.Context, tx workspaceTransaction, frameID string, extra map[string]any) error {
	// A response-only availability hint is never durable presentation authority,
	// including when an existing legacy map is merged by a real CAS command.
	stored := make(map[string]any, len(extra))
	for key, value := range extra {
		if key != "presentation_unavailable" {
			stored[key] = value
		}
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO frame_runtime_metadata(frame_id,context_data) VALUES(?,json_object('web_extra',json(?)))
		ON CONFLICT(frame_id) DO UPDATE SET context_data=json_set(COALESCE(frame_runtime_metadata.context_data,'{}'),'$.web_extra',json(?))`, frameID, string(encoded), string(encoded))
	return err
}

// SQL consumers apply visibility before their cursor/order/LIMIT. The column
// expression is an internal SQL identifier, never caller-controlled input.
func conversationUnarchivedSQL(contextColumn string) string {
	return "COALESCE(json_extract(" + contextColumn + ",'$.web_extra.archived_at'),0)=0"
}
