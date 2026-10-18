package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Shared by the canonical producer and the retained legacy journal adapter.
func KernelDelegationInitialText(child KernelSupervisedChild) string {
	text := child.Task
	if strings.TrimSpace(child.ContextSummary) != "" {
		text = "Context:\n" + child.ContextSummary + "\n\nTask:\n" + child.Task
	}
	if len(child.OutputSchema) > 0 {
		raw, _ := json.Marshal(child.OutputSchema)
		text += "\n\nSubmit a compact result matching this JSON Schema with submit_output before finishing:\n" + string(raw)
	}
	return text
}

func (s *Store) createDelegatedInitialInputTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, child KernelSupervisedChild, incarnation string, admission *KernelDelegationAdmission) error {
	scope, _, err := delegatedScopeTx(ctx, tx, child.OwnerUserID, child.ParentFrameID)
	if err != nil {
		return err
	}
	stream, err := tx.CreateStream(ctx, transcriptstore.CreateStreamInput{UID: "frame:" + child.FrameID, OwnerID: child.OwnerUserID,
		ExternalID: child.FrameID, SessionID: child.FrameID, Kind: transcriptstore.StreamKindFrameRef,
		ProjectID: child.ProjectID, RootFrameID: child.RootFrameID, FrameID: child.FrameID, Epoch: 1})
	if err != nil {
		return err
	}
	clientID := "kernel-delegate-task:" + child.ToolUseID + ":" + child.FrameID
	_, _, created, err := tx.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: child.OwnerUserID, ClientMessageID: clientID,
		FrameEventID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("kernel-delegate-user:"+child.ToolUseID+":"+child.FrameID)).String(),
		MessageUUID:  "delegate-task-" + child.FrameID, Text: KernelDelegationInitialText(child),
		RuntimeConfig: map[string]any{"agentName": child.AgentName, "model": child.Model}, Destinations: []string{"ws"},
	})
	if err != nil {
		return err
	}
	if !created {
		return transcriptstore.ErrEventConflict
	}
	_, epoch, revision, err := delegatedStreamTx(ctx, tx, child.OwnerUserID, child.FrameID)
	if err != nil {
		return err
	}
	record := delegationOriginRecord{Version: 1, OwnerID: child.OwnerUserID, Scope: scope, ToolUseID: child.ToolUseID,
		InitialTarget: DelegatedStopTarget{FrameID: child.FrameID, FrameIncarnationID: incarnation, StreamUID: stream.UID,
			StreamEpoch: epoch, InputMessageID: clientID, InputRevision: revision}}
	if admission != nil {
		record.AdmissionID = admission.ID
	}
	return writeDelegatedRecord(ctx, tx, child.FrameID, delegatedRecordID("origin", child.FrameID, incarnation), delegationOriginEventType, "", record, s.now().UTC())
}

// True means exactly this child's immutable initial input is already canonical,
// not merely that some stream exists. A missing legacy origin stays legacy.
func (s *Store) KernelChildHasCanonicalInitialInput(ctx context.Context, child KernelSupervisedChild) (bool, error) {
	target, err := s.KernelChildInitialTarget(ctx, child)
	return target != nil, err
}

func (s *Store) KernelChildInitialTarget(ctx context.Context, child KernelSupervisedChild) (*DelegatedStopTarget, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var incarnation string
	err = tx.QueryRowContext(ctx, `SELECT incarnation_id FROM frames WHERE id=? AND parent_frame_id=? AND root_frame_id=? AND project_id=?`, child.FrameID, child.ParentFrameID, child.RootFrameID, child.ProjectID).Scan(&incarnation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDelegatedStopUnavailable
	}
	if err != nil {
		return nil, err
	}
	var record delegationOriginRecord
	_, found, err := readDelegatedRecord(ctx, tx, child.FrameID, delegatedRecordID("origin", child.FrameID, incarnation), delegationOriginEventType, &record)
	if err != nil || !found {
		return nil, err
	}
	scope, _, err := delegatedScopeTx(ctx, tx, child.OwnerUserID, child.ParentFrameID)
	if err != nil {
		return nil, err
	}
	if record.Version != 1 || record.OwnerID != child.OwnerUserID || record.Scope != scope || record.ToolUseID != child.ToolUseID || record.InitialTarget.FrameID != child.FrameID || record.InitialTarget.FrameIncarnationID != incarnation {
		return nil, ErrDelegatedStopScopeStale
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT event.payload_json FROM transcript_events event
		JOIN transcript_input_admissions admission ON admission.stream_uid=event.stream_uid AND admission.event_id=event.event_id
		JOIN transcript_streams stream ON stream.stream_uid=event.stream_uid
		WHERE event.stream_uid=? AND event.client_message_id=? AND admission.input_revision=? AND admission.source_kind='event'
		AND admission.input_kind='message' AND admission.classification_version=1 AND stream.owner_id=? AND stream.frame_id=? AND stream.epoch=?`,
		record.InitialTarget.StreamUID, record.InitialTarget.InputMessageID, record.InitialTarget.InputRevision, child.OwnerUserID, child.FrameID, record.InitialTarget.StreamEpoch).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDelegatedStopOriginUnavailable
	}
	if err != nil {
		return nil, err
	}
	var payload struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil || payload.Text != strings.TrimSpace(KernelDelegationInitialText(child)) {
		return nil, ErrDelegatedStopOriginUnavailable
	}
	return &record.InitialTarget, tx.Commit()
}
