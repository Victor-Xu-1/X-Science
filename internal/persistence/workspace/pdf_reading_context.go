package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

type PDFReadingMutation struct {
	OwnerID     string
	FrameID     string
	OperationID string
	Expected    transcriptstore.PDFReadingExpected
	Bindings    []transcriptstore.PDFReadingBinding
	Sources     []transcriptstore.PDFReadingSource
	BindingID   string
}

func (s *Store) GetPDFReadingContext(ctx context.Context, owner, frame string) (transcriptstore.PDFReadingContext, error) {
	if s == nil || s.readDatabase() == nil {
		return transcriptstore.PDFReadingContext{}, ErrWorkspaceStoreClosed
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return transcriptstore.PDFReadingContext{}, err
	}
	defer tx.Rollback()
	result, err := transcriptstore.ReadPDFReadingContext(ctx, tx, owner, frame)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func PDFReadingBindingID(scope transcriptstore.PDFReadingScope, operation, artifact, version string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("x-science:pdf-reading:"+scope.FrameID+"\x00"+scope.FrameIncarnationID+"\x00"+
		scope.RootFrameIncarnationID+"\x00"+operation+"\x00"+artifact+"\x00"+version)).String()
}

func (s *Store) LinkPDFReadingContext(ctx context.Context, input PDFReadingMutation) (transcriptstore.PDFReadingContext, error) {
	return s.mutatePDFReadingContext(ctx, input, true)
}

func (s *Store) UnlinkPDFReadingContext(ctx context.Context, input PDFReadingMutation) (transcriptstore.PDFReadingContext, error) {
	return s.mutatePDFReadingContext(ctx, input, false)
}

func (s *Store) mutatePDFReadingContext(ctx context.Context, input PDFReadingMutation, link bool) (transcriptstore.PDFReadingContext, error) {
	var result transcriptstore.PDFReadingContext
	operation, err := uuid.Parse(input.OperationID)
	if err != nil || operation.String() != input.OperationID || !input.Expected.Valid() || input.OwnerID == "" ||
		input.FrameID == "" || link && (len(input.Sources) < 1 || len(input.Sources) > transcriptstore.MaxPDFReadingBindings) ||
		!link && (input.BindingID == "" || len(input.BindingID) > 512) {
		return result, transcriptstore.ErrPDFReadingInvalid
	}
	repo, err := s.TranscriptRepository(ctx)
	if err != nil {
		return result, err
	}
	err = repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var err error
		result, err = transcriptstore.ReadPDFReadingContext(ctx, tx, input.OwnerID, input.FrameID)
		if err != nil {
			return err
		}
		replay, err := PDFReadingMutationReplay(result, input, link)
		if err != nil || replay {
			return err
		}
		if err := transcriptstore.CheckFrameConversationAvailable(ctx, tx, input.FrameID); err != nil {
			return err
		}
		if link {
			if len(input.Bindings) != len(input.Sources) {
				return transcriptstore.ErrPDFReadingInvalid
			}
			versions := map[string]bool{}
			for _, binding := range result.Bindings {
				versions[binding.Source().Key()] = true
			}
			for index, binding := range input.Bindings {
				if !binding.Matches(input.Sources[index]) {
					return transcriptstore.ErrPDFReadingInvalid
				}
				if !binding.Valid() {
					return transcriptstore.ErrPDFReadingInvalid
				}
				if versions[binding.Source().Key()] {
					continue
				}
				if err := transcriptstore.ValidatePDFReadingSnapshotSources(ctx, tx, input.OwnerID, transcriptstore.PDFReadingSnapshot{
					Scope: result.Scope, Bindings: []transcriptstore.PDFReadingBinding{binding},
				}); err != nil {
					return err
				}
				if binding.SourceKind == "literature-attachment-version" {
					var live bool
					if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM literature_items WHERE id=? AND owner_user_id=? AND deleted_at IS NULL)`, binding.ItemID, input.OwnerID).Scan(&live); err != nil {
						return err
					}
					if !live {
						return transcriptstore.ErrPDFReadingUnavailable
					}
				}
				identity := binding.ArtifactID
				if binding.SourceKind != "artifact-version" {
					identity = binding.Source().Key() + "\x00" + binding.ItemID + "\x00" + binding.AttachmentID
				}
				binding.BindingID = PDFReadingBindingID(result.Scope, input.OperationID, identity, binding.VersionID)
				binding.LinkedAt = s.now().UTC().Format(time.RFC3339Nano)
				result.Bindings = append(result.Bindings, binding)
				versions[binding.Source().Key()] = true
				result.Changed = true
			}
			if len(result.Bindings) > transcriptstore.MaxPDFReadingBindings {
				return transcriptstore.ErrPDFReadingLimit
			}
		} else {
			kept := make([]transcriptstore.PDFReadingBinding, 0, len(result.Bindings))
			for _, binding := range result.Bindings {
				if binding.BindingID == input.BindingID {
					result.Changed = true
				} else {
					kept = append(kept, binding)
				}
			}
			if !result.Changed {
				return transcriptstore.ErrPDFReadingStale
			}
			result.Bindings = kept
		}
		if !result.Changed {
			return nil
		}
		if result.Revision == 9007199254740991 {
			return transcriptstore.ErrPDFReadingLimit
		}
		result.Revision++
		persisted := result
		persisted.Changed = false
		proof := pdfReadingMutationProof(input, link)
		result.LastMutation = &proof
		raw, err := json.Marshal(transcriptstore.PDFReadingRecord{OwnerID: input.OwnerID, Context: persisted, LastMutation: &proof})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO frame_runtime_metadata(frame_id,context_data) VALUES(?,json_object('_pdf_reading_context',json(?)))
   ON CONFLICT(frame_id) DO UPDATE SET context_data=json_set(
    CASE WHEN json_type(frame_runtime_metadata.context_data)='null' THEN '{}' ELSE frame_runtime_metadata.context_data END,
    '$._pdf_reading_context',json(?))`,
			input.FrameID, string(raw), string(raw))
		if err != nil {
			return err
		}
		return s.enqueuePDFReadingChange(ctx, tx, input, result)
	})
	return result, err
}

func pdfReadingMutationProof(input PDFReadingMutation, link bool) transcriptstore.PDFReadingMutationProof {
	kind := "unlink"
	if link {
		kind = "link"
	}
	raw, _ := json.Marshal(struct {
		OwnerID     string
		FrameID     string
		OperationID string
		Kind        string
		Expected    transcriptstore.PDFReadingExpected
		Sources     []transcriptstore.PDFReadingSource
		BindingID   string
	}{input.OwnerID, input.FrameID, input.OperationID, kind, input.Expected, input.Sources, input.BindingID})
	digest := sha256.Sum256(raw)
	return transcriptstore.PDFReadingMutationProof{OperationID: input.OperationID, Kind: kind, ExpectedRevision: *input.Expected.Revision, RequestSHA256: fmt.Sprintf("%x", digest)}
}

// One current-record replay, checked before mutable content inspection.
func PDFReadingMutationReplay(current transcriptstore.PDFReadingContext, input PDFReadingMutation, link bool) (bool, error) {
	if !input.Expected.Matches(current.Scope) {
		return false, transcriptstore.ErrPDFReadingScope
	}
	proof := pdfReadingMutationProof(input, link)
	if current.LastMutation != nil && *current.LastMutation == proof && current.Revision == proof.ExpectedRevision+1 {
		return true, nil
	}
	if *input.Expected.Revision != current.Revision {
		return false, transcriptstore.ErrPDFReadingStale
	}
	return false, nil
}
