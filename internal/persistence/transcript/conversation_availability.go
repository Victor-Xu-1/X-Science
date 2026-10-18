package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
)

var ErrConversationArchived = errors.New("conversation is archived; restore it before starting work")
var ErrConversationArchiveInvalid = errors.New("conversation archive state is invalid")

// ConversationArchivedAt reads the one protected presentation marker. A bad
// marker is not permission to resume work or overwrite the durable metadata.
func ConversationArchivedAt(contextData map[string]any) (int64, error) {
	container, present := contextData["web_extra"]
	extra, object := container.(map[string]any)
	if present && container != nil && !object {
		return 0, ErrConversationArchiveInvalid
	}
	value := extra["archived_at"]
	if value == nil {
		return 0, nil
	}
	var number float64
	switch value := value.(type) {
	case int64:
		number = float64(value)
	case int:
		number = float64(value)
	case float64:
		number = value
	case json.Number:
		var err error
		number, err = value.Float64()
		if err != nil {
			return 0, ErrConversationArchiveInvalid
		}
	default:
		return 0, ErrConversationArchiveInvalid
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 9007199254740991 || math.Trunc(number) != number {
		return 0, ErrConversationArchiveInvalid
	}
	return int64(number), nil
}

// CheckFrameConversationAvailable must run inside the admission/claim write
// transaction. Both root and descendant work use the root's durable marker;
// callers retain their existing owner/incarnation/branch/lease validation.
func CheckFrameConversationAvailable(ctx context.Context, db MessageIntentQuery, frameID string) error {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT COALESCE(metadata.context_data,'{}')
		FROM frames frame JOIN frames root ON root.id=frame.root_frame_id
		LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=root.id WHERE frame.id=?`, frameID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEventConflict
	}
	if err != nil {
		return err
	}
	var contextData map[string]any
	if json.Unmarshal([]byte(raw), &contextData) != nil {
		return ErrConversationArchiveInvalid
	}
	archivedAt, err := ConversationArchivedAt(contextData)
	if err != nil {
		return err
	}
	if archivedAt > 0 {
		return ErrConversationArchived
	}
	return nil
}
