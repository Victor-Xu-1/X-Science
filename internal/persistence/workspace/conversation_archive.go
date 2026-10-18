package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

var ErrConversationArchiveBusy = errors.New("conversation has work that must finish or be resolved before archiving")

type ConversationArchiveUpdate struct {
	OwnerID, FrameID, IncarnationID string
	ExpectedRevision                int64
	Archived                        bool
}

type ConversationArchiveResult struct {
	Changed     bool
	Frame       CompatibilityFrame
	ProjectName string
}

// UpdateConversationArchive is the sole archive command. Immediate write
// serialization is shared with final input admission and runner claims.
func (s *Store) UpdateConversationArchive(ctx context.Context, input ConversationArchiveUpdate) (ConversationArchiveResult, error) {
	if s == nil || s.db == nil {
		return ConversationArchiveResult{}, ErrWorkspaceStoreClosed
	}
	if strings.TrimSpace(input.OwnerID) == "" || strings.TrimSpace(input.FrameID) == "" || strings.TrimSpace(input.IncarnationID) == "" ||
		input.ExpectedRevision < 0 || input.ExpectedRevision > 9007199254740991 {
		return ConversationArchiveResult{}, ErrConversationPresentationInvalid
	}
	var result ConversationArchiveResult
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var owner, incarnation, raw, projectName string
		var parent sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT project.user_id,frame.incarnation_id,frame.parent_frame_id,COALESCE(metadata.context_data,'{}'),project.name
			FROM frames frame JOIN projects project ON project.id=frame.project_id
			LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=frame.id WHERE frame.id=?`, input.FrameID).Scan(&owner, &incarnation, &parent, &raw, &projectName)
		if errors.Is(err, sql.ErrNoRows) || err == nil && owner != input.OwnerID {
			return transcriptstore.ErrOwnerMismatch
		}
		if err != nil {
			return err
		}
		if incarnation != input.IncarnationID || parent.Valid && parent.String != "" {
			return ErrConversationPresentationStale
		}
		capture := func() error {
			frame, err := scanTraceFrame(tx.QueryRowContext(ctx, traceFrameSelectColumns+` WHERE frame.id=?`, input.FrameID))
			if err != nil {
				return err
			}
			result.Frame = CompatibilityFrame{Frame: frame, TaskSummary: frame.TaskSummary, InputData: frame.InputData, IsHidden: frame.IsHidden}
			result.ProjectName = projectName
			return nil
		}
		var contextData map[string]any
		if json.Unmarshal([]byte(raw), &contextData) != nil || contextData == nil {
			return ErrConversationPresentationInvalid
		}
		value, present := contextData["web_extra"]
		extra, object := value.(map[string]any)
		if present && value != nil && !object {
			return ErrConversationPresentationInvalid
		}
		if extra == nil {
			extra = map[string]any{}
		}
		revision, err := presentationRevision(extra)
		if err != nil {
			return err
		}
		// An old receipt must never archive a later restored incarnation, even
		// if its requested value currently happens to match. Check before noop.
		if input.ExpectedRevision != revision {
			return ErrConversationPresentationStale
		}
		archivedAt, err := transcriptstore.ConversationArchivedAt(contextData)
		if err != nil {
			return err
		}
		if (archivedAt > 0) == input.Archived {
			return capture()
		}
		if revision >= 9007199254740991 {
			return ErrConversationPresentationInvalid
		}
		if input.Archived {
			if err := s.assertConversationArchiveIdleTx(ctx, tx, input.FrameID, s.now().UTC()); err != nil {
				return err
			}
			extra["archived_at"] = s.now().UTC().UnixMilli()
		} else {
			delete(extra, "archived_at")
		}
		extra[presentationRevisionKey] = revision + 1
		if err := storeConversationPresentationExtraTx(ctx, tx, input.FrameID, extra); err != nil {
			return err
		}
		result.Changed = true
		return capture()
	})
	if err != nil {
		return ConversationArchiveResult{}, err
	}
	return result, nil
}
