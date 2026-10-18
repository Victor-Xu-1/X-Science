package transcript

import (
	"context"
	"encoding/json"
	"reflect"
)

// Absence means no reading binding, including when the session currently has
// PDFs. Only an explicit frozen selection can add reading evidence to a send.
func pdfReadingSelectionFromInput(data map[string]any) (*PDFReadingSelection, error) {
	selection, err := DecodePDFReadingSelection(data[PDFReadingSelectionKey])
	if err != nil {
		return nil, err
	}
	if selection != nil || data[PDFReadingSnapshotKey] != nil {
		if intent, _ := data["turn_intent"].(string); intent != "" {
			return nil, ErrPDFReadingInvalid
		}
	}
	return selection, nil
}

func readingSnapshotValue(snapshot PDFReadingSnapshot) map[string]any {
	raw, _ := json.Marshal(snapshot)
	value := map[string]any{}
	_ = json.Unmarshal(raw, &value)
	return value
}

func readingInputWithSnapshot(data map[string]any, snapshot PDFReadingSnapshot) map[string]any {
	result := make(map[string]any, len(data)+1)
	for key, value := range data {
		result[key] = value
	}
	result[PDFReadingSnapshotKey] = readingSnapshotValue(snapshot)
	return result
}

func snapshotPDFReadingReferences(snapshot PDFReadingSnapshot) []UserArtifactReferenceInput {
	refs := make([]UserArtifactReferenceInput, 0, len(snapshot.Bindings))
	for _, binding := range snapshot.Bindings {
		refs = append(refs, UserArtifactReferenceInput{ArtifactID: binding.ArtifactID, VersionID: binding.VersionID})
	}
	return refs
}

func MergePDFReadingArtifactReferences(refs, extra []UserArtifactReferenceInput) ([]UserArtifactReferenceInput, error) {
	result := append([]UserArtifactReferenceInput(nil), refs...)
	seen := map[string]bool{}
	for _, ref := range result {
		seen[ref.ArtifactID+"\x00"+ref.VersionID] = true
	}
	for _, ref := range extra {
		key := ref.ArtifactID + "\x00" + ref.VersionID
		if !seen[key] {
			result = append(result, ref)
			seen[key] = true
		}
	}
	return normalizeUserArtifactReferenceInputs(result)
}

// Called inside the existing queue-acceptance transaction, before its event and
// envelope are inserted. Caller-authored snapshots can never become receipts.
func FreezePDFReadingQueueInput(ctx context.Context, query MessageIntentQuery, owner, frame string, data map[string]any) (map[string]any, []UserArtifactReferenceInput, error) {
	if _, supplied := data[PDFReadingSnapshotKey]; supplied {
		return nil, nil, ErrPDFReadingInvalid
	}
	selection, err := pdfReadingSelectionFromInput(data)
	if err != nil || selection == nil {
		return data, nil, err
	}
	current, err := ReadPDFReadingContext(ctx, query, owner, frame)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := FreezePDFReadingSelection(current, *selection)
	if err != nil {
		return nil, nil, err
	}
	if err = ValidatePDFReadingSnapshotSources(ctx, query, owner, snapshot); err != nil {
		return nil, nil, err
	}
	return readingInputWithSnapshot(data, snapshot), snapshotPDFReadingReferences(snapshot), nil
}

// Final append shares its transaction with this guard. Accepted event replay
// and a real queued envelope are checked before mutable current context.
func resolvePDFReadingMessageInput(ctx context.Context, query MessageIntentQuery, stream Stream, clientID string, data map[string]any, existing *Event) (map[string]any, []UserArtifactReferenceInput, error) {
	selection, err := pdfReadingSelectionFromInput(data)
	if err != nil {
		return nil, nil, err
	}
	if selection == nil && data[PDFReadingSnapshotKey] == nil {
		return data, nil, nil
	}
	if existing != nil {
		var original struct {
			Input map[string]any `json:"inputData"`
		}
		if existing.Source != EventSourcePayload || json.Unmarshal(existing.PayloadJSON, &original) != nil {
			return nil, nil, ErrPDFReadingInvalid
		}
		frozen, err := decodePDFReadingSnapshot(original.Input[PDFReadingSnapshotKey])
		if err != nil {
			return nil, nil, err
		}
		prior, err := DecodePDFReadingSelection(original.Input[PDFReadingSelectionKey])
		if err != nil || !reflect.DeepEqual(selection, prior) {
			return nil, nil, ErrMessageIntentConflict
		}
		if supplied := data[PDFReadingSnapshotKey]; supplied != nil {
			candidate, err := decodePDFReadingSnapshot(supplied)
			if err != nil || !reflect.DeepEqual(candidate, frozen) {
				return nil, nil, ErrMessageIntentConflict
			}
		}
		return readingInputWithSnapshot(data, *frozen), snapshotPDFReadingReferences(*frozen), nil
	}
	if data[PDFReadingSnapshotKey] != nil {
		frozen, err := readQueuedPDFReadingSnapshot(ctx, query, stream.OwnerID, stream.FrameID, clientID, data)
		if err != nil {
			return nil, nil, err
		}
		return readingInputWithSnapshot(data, *frozen), snapshotPDFReadingReferences(*frozen), nil
	}
	return FreezePDFReadingQueueInput(ctx, query, stream.OwnerID, stream.FrameID, data)
}

func ValidatePDFReadingSnapshotSources(ctx context.Context, query MessageIntentQuery, owner string, snapshot PDFReadingSnapshot) error {
	for _, binding := range snapshot.Bindings {
		var valid bool
		err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_versions version
   JOIN artifacts artifact ON artifact.id=version.artifact_id JOIN projects project ON project.id=artifact.project_id
   WHERE version.id=? AND artifact.id=? AND project.id=? AND project.user_id=? AND lower(version.content_sha256)=?
    AND CASE WHEN COALESCE(version.storage_path,'')='' THEN length(version.content) ELSE version.size_bytes END=?)`,
			binding.VersionID, binding.ArtifactID, snapshot.Scope.ProjectID, owner, binding.Checksum, binding.SizeBytes).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return ErrPDFReadingUnavailable
		}
	}
	return nil
}
