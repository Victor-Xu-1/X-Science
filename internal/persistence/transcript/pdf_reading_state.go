package transcript

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

func ReadPDFReadingContext(ctx context.Context, query MessageIntentQuery, owner, frameID string) (PDFReadingContext, error) {
	result := PDFReadingContext{Schema: PDFReadingContextSchema, Bindings: []PDFReadingBinding{}}
	scope, err := ReadPDFReadingScope(ctx, query, owner, frameID)
	if err != nil {
		return result, err
	}
	result.Scope = scope
	var raw []byte
	err = query.QueryRowContext(ctx, `SELECT COALESCE(context_data,'{}') FROM frame_runtime_metadata WHERE frame_id=?`, frameID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(raw, &metadata) != nil {
		return result, ErrPDFReadingState
	}
	stored, exists := metadata[PDFReadingMetadataKey]
	if !exists {
		return result, nil
	}
	var record PDFReadingRecord
	if json.Unmarshal(stored, &record) != nil || record.Context.Schema != PDFReadingContextSchema ||
		record.Context.Revision < 0 || record.Context.Revision > 9007199254740991 ||
		record.Context.Bindings == nil || len(record.Context.Bindings) > MaxPDFReadingBindings {
		return result, ErrPDFReadingState
	}
	if record.OwnerID != owner || record.Context.Scope != result.Scope {
		return result, ErrPDFReadingScope
	}
	seen, versions := map[string]bool{}, map[string]bool{}
	for _, binding := range record.Context.Bindings {
		if !binding.Valid() || seen[binding.BindingID] || versions[binding.Source().Key()] {
			return result, ErrPDFReadingState
		}
		seen[binding.BindingID], versions[binding.Source().Key()] = true, true
	}
	if record.LastMutation == nil {
		return result, ErrPDFReadingState
	}
	proof := record.LastMutation
	id, parseErr := uuid.Parse(proof.OperationID)
	digest, hashErr := hex.DecodeString(proof.RequestSHA256)
	if parseErr != nil || id.String() != proof.OperationID || hashErr != nil || len(digest) != 32 ||
		proof.Kind != "link" && proof.Kind != "unlink" || proof.ExpectedRevision < 0 || proof.ExpectedRevision+1 != record.Context.Revision {
		return result, ErrPDFReadingState
	}
	record.Context.Changed = false
	record.Context.LastMutation = record.LastMutation
	return record.Context, nil
}

func ReadPDFReadingScope(ctx context.Context, query MessageIntentQuery, owner, frameID string) (PDFReadingScope, error) {
	var scope PDFReadingScope
	var actualOwner string
	err := query.QueryRowContext(ctx, `SELECT project.user_id,frame.id,frame.incarnation_id,frame.root_frame_id,root.incarnation_id,frame.project_id
  FROM frames frame JOIN projects project ON project.id=frame.project_id
  JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id WHERE frame.id=?`, frameID).
		Scan(&actualOwner, &scope.FrameID, &scope.FrameIncarnationID, &scope.RootFrameID, &scope.RootFrameIncarnationID, &scope.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, ErrOwnerMismatch
	}
	if err != nil {
		return scope, err
	}
	if owner == "" || owner != actualOwner {
		return scope, ErrOwnerMismatch
	}
	if !scope.Valid() {
		return scope, ErrPDFReadingScope
	}
	return scope, nil
}

func FreezePDFReadingSelection(current PDFReadingContext, selection PDFReadingSelection) (PDFReadingSnapshot, error) {
	snapshot := PDFReadingSnapshot{Schema: PDFReadingSnapshotSchema, Scope: current.Scope, Revision: current.Revision, Bindings: []PDFReadingBinding{},
		ActiveBindingID: selection.ActiveBindingID, PageNumber: selection.PageNumber}
	if selection.Validate() != nil {
		return snapshot, ErrPDFReadingInvalid
	}
	if !selection.PDFReadingExpected.Matches(current.Scope) {
		return snapshot, ErrPDFReadingScope
	}
	if *selection.Revision != current.Revision {
		return snapshot, ErrPDFReadingStale
	}
	byID := map[string]PDFReadingBinding{}
	for _, binding := range current.Bindings {
		byID[binding.BindingID] = binding
	}
	for _, id := range selection.BindingIDs {
		binding, found := byID[id]
		if !found {
			return snapshot, ErrPDFReadingStale
		}
		if id == selection.ActiveBindingID && selection.PageNumber > binding.PageCount {
			return snapshot, ErrPDFReadingInvalid
		}
		snapshot.Bindings = append(snapshot.Bindings, binding)
	}
	return snapshot, nil
}
