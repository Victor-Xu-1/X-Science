package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Generic runtime updates can round-trip a previously read value, but never
// replace its authority. A stale runtime snapshot preserves the newer binding
// state instead of creating a new failure gate for unrelated running work.
func preservePDFReadingMetadata(ctx context.Context, tx workspaceTransaction, frame string, incoming map[string]any) (map[string]any, error) {
	result := make(map[string]any)
	for key, value := range incoming {
		result[key] = value
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT context_data FROM frame_runtime_metadata WHERE frame_id=?`, frame).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	stored := map[string]json.RawMessage{}
	if err == nil && json.Unmarshal(raw, &stored) != nil {
		return nil, transcriptstore.ErrPDFReadingState
	}
	if stored == nil {
		stored = map[string]json.RawMessage{}
	}
	if value, found := stored[transcriptstore.PDFReadingMetadataKey]; found {
		result[transcriptstore.PDFReadingMetadataKey] = value
	} else if _, supplied := incoming[transcriptstore.PDFReadingMetadataKey]; supplied {
		return nil, transcriptstore.ErrPDFReadingInvalid
	}
	return result, nil
}
