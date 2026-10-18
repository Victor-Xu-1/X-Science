package workspace

import (
	"context"
	"encoding/json"

	transcriptstore "x-science/internal/persistence/transcript"
)

func freezeQueuedPDFReadingContext(ctx context.Context, tx workspaceTransaction, frame string, delivery map[string]any) (map[string]any, error) {
	data, ok := delivery["inputData"].(map[string]any)
	if !ok || data[transcriptstore.PDFReadingSelectionKey] == nil && data[transcriptstore.PDFReadingSnapshotKey] == nil {
		return delivery, nil
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT project.user_id FROM frames frame JOIN projects project ON project.id=frame.project_id WHERE frame.id=?`, frame).Scan(&owner); err != nil {
		return nil, err
	}
	frozen, extra, err := transcriptstore.FreezePDFReadingQueueInput(ctx, tx, owner, frame, data)
	if err != nil {
		return nil, err
	}
	var refs []transcriptstore.UserArtifactReferenceInput
	if value := delivery["artifactRefs"]; value != nil {
		raw, err := json.Marshal(value)
		if err != nil || json.Unmarshal(raw, &refs) != nil {
			return nil, transcriptstore.ErrPDFReadingInvalid
		}
	}
	refs, err = transcriptstore.MergePDFReadingArtifactReferences(refs, extra)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any)
	for key, value := range delivery {
		result[key] = value
	}
	result["inputData"] = frozen
	if len(refs) > 0 {
		result["artifactRefs"] = refs
	}
	return result, nil
}
