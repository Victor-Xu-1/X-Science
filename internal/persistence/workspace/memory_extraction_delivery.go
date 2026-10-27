package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

const (
	MemoryExtractionOutboxTopic  = "memory.extraction"
	MemoryExtractionOutboxType   = "memory.extraction.completed.v1"
	MemoryExtractionViewMaxBytes = 768 << 10
)

// These private-topic values contain no provider endpoint, credential or host
// grant. The profile digest detects a changed endpoint without retaining it.
type MemoryExtractionModelIdentity struct {
	Model         string `json:"model"`
	Selection     string `json:"selection,omitempty"`
	ProviderID    string `json:"provider_id,omitempty"`
	ProfileSHA256 string `json:"profile_sha256,omitempty"`
}

type MemoryExtractionCompletion struct {
	Mode              string                        `json:"mode"`
	ModeDecision      *MemoryExtractionModeDecision `json:"mode_decision,omitempty"`
	ExtractEveryN     int                           `json:"extract_every_n"`
	Model             MemoryExtractionModelIdentity `json:"model"`
	RequestView       json.RawMessage               `json:"request_view,omitempty"`
	RequestViewSHA256 string                        `json:"request_view_sha256,omitempty"`
	Diagnostic        string                        `json:"diagnostic,omitempty"`
}

type MemoryExtractionDispatch struct {
	Version    int                        `json:"version"`
	Source     CompletedExtractionSource  `json:"source"`
	SessionID  string                     `json:"session_id"`
	Completion MemoryExtractionCompletion `json:"completion"`
	Cadence    MemoryExtractionCadence    `json:"cadence"`
}

type MemoryExtractionCadence struct {
	EveryN   int   `json:"every_n"`
	Ordinal  int64 `json:"ordinal"`
	Eligible bool  `json:"eligible"`
}

// Claim is current transport authority only; it is never part of source,
// extraction-input or mutation-plan digests, nor the immutable Memory receipt.
type MemoryExtractionDeliveryClaim struct {
	EventID, ClaimOwner, ClaimToken, SourceKey string
}

func memoryExtractionPartition(identity extractionSourceIdentity) string {
	return "memory-extraction:" + extractionDigest([]byte(identity.source.OwnerUserID+"\x00"+identity.source.FrameID+"\x00"+identity.incarnation))
}

// FinishRunnerWithMemoryExtraction is the only canonical automatic producer.
// The caller has a nonempty root chat reply; SQL independently rejects other
// completion kinds. Terminal settlement and enqueue share one immediate TX.
func (s *Store) FinishRunnerWithMemoryExtraction(ctx context.Context, input transcriptstore.FinishRunnerInput, completion MemoryExtractionCompletion) (transcriptstore.Event, transcriptstore.FinishReceipt, bool, error) {
	repository, err := s.TranscriptRepository(ctx)
	if err != nil {
		return transcriptstore.Event{}, transcriptstore.FinishReceipt{}, false, err
	}
	var event transcriptstore.Event
	var receipt transcriptstore.FinishReceipt
	var created bool
	err = repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var err error
		event, receipt, created, err = tx.FinishRunner(ctx, input)
		if err != nil {
			return err
		}
		_, err = s.enqueueCompletedRunnerMemoryTx(ctx, tx, input, event, created, completion)
		return err
	})
	return event, receipt, created, err
}

// The canonical eligibility/cadence/enqueue producer is shared with atomic final
// answer settlement. It uses only the caller's already-held transaction.
func (s *Store) enqueueCompletedRunnerMemoryTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction,
	input transcriptstore.FinishRunnerInput, event transcriptstore.Event, created bool, completion MemoryExtractionCompletion,
) (*OutboxEvent, error) {
	if !created || input.Status != "completed" {
		return nil, nil
	}
	var terminal struct {
		Purpose string `json:"purpose"`
	}
	if json.Unmarshal(event.PayloadJSON, &terminal) != nil {
		return nil, errors.New("invalid completed Memory terminal")
	}
	if terminal.Purpose == transcriptstore.ContextCompactionPurpose {
		return nil, nil
	}
	proof, err := tx.ReadCompletedMemorySource(ctx, input.Claim.StreamUID, input.Claim.OwnerID, event.EventID)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, transcriptstore.ErrEventConflict) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if proof.FrameID != proof.RootFrameID {
		return nil, nil
	}
	var project, parent string
	var hasReply bool
	if err := tx.QueryRowContext(ctx, `SELECT frame.project_id,COALESCE(frame.parent_frame_id,''),EXISTS(
	 SELECT 1 FROM transcript_events message JOIN transcript_branch_events member
	 ON member.stream_uid=message.stream_uid AND member.event_id=message.event_id AND member.branch_id=?
	 WHERE message.stream_uid=? AND message.runner_attempt=? AND message.event_type='assistant_message'
	 AND message.publication_seq<? AND length(trim(COALESCE(json_extract(message.payload_json,'$.text'),'')))>0)
	 FROM frames frame WHERE frame.id=?`, proof.BranchID, proof.StreamUID, proof.Attempt, proof.PublicationSeq, proof.FrameID).Scan(&project, &parent, &hasReply); err != nil {
		return nil, err
	}
	if parent != "" || !hasReply {
		return nil, nil
	}
	source := CompletedExtractionSource{Schema: 1, OwnerUserID: proof.OwnerID, ProjectID: project, FrameID: proof.FrameID, Transcript: &proof}
	identity, err := identifyExtractionSource(source)
	if err != nil {
		return nil, err
	}
	if err := requireExtractionPolicyTx(ctx, tx, source); errors.Is(err, ErrAgentMemoryMutationForbidden) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := validateMemoryExtractionCompletion(completion, source); err != nil {
		return nil, err
	}
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO project_runtime_metadata(project_id,memory_completion_ordinal)
	 SELECT id,1 FROM projects WHERE id=? AND user_id=?
	 ON CONFLICT(project_id) DO UPDATE SET memory_completion_ordinal=project_runtime_metadata.memory_completion_ordinal+1
	 WHERE project_runtime_metadata.memory_completion_ordinal<9223372036854775807 RETURNING memory_completion_ordinal`, source.ProjectID, source.OwnerUserID).Scan(&ordinal); err != nil {
		return nil, err
	}
	cadence := MemoryExtractionCadence{EveryN: completion.ExtractEveryN, Ordinal: ordinal, Eligible: ordinal%int64(completion.ExtractEveryN) == 0}
	dispatch := MemoryExtractionDispatch{Version: 1, Source: source, SessionID: proof.FrameID, Completion: completion, Cadence: cadence}
	payload, err := json.Marshal(dispatch)
	if err != nil {
		return nil, err
	}
	if len(payload) > outboxMaxPayloadBytes {
		return nil, errors.New("bounded Memory dispatch exceeds transport limit")
	}
	outbox, err := s.enqueueOutboxTransaction(ctx, tx, EnqueueOutboxInput{
		Topic: MemoryExtractionOutboxTopic, Type: MemoryExtractionOutboxType, IdempotencyKey: identity.key,
		PartitionKey: memoryExtractionPartition(identity), AggregateType: "frame", AggregateID: source.FrameID, Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	return &outbox, nil
}

func validateMemoryExtractionCompletion(value MemoryExtractionCompletion, sources ...CompletedExtractionSource) error {
	if value.Mode != "forked" && value.Mode != "compact" {
		return errors.New("invalid Memory extraction mode")
	}
	if value.ModeDecision != nil {
		if len(sources) != 1 || value.ModeDecision.Validate(sources[0]) != nil ||
			value.ModeDecision.EffectiveMode != value.Mode || value.ModeDecision.ModelProfileSHA256 != value.Model.ProfileSHA256 {
			return errors.New("Memory mode decision does not match the captured source/model")
		}
		if value.ModeDecision.Recovered() && (value.Diagnostic != "" || len(value.RequestView) != 0 ||
			value.RequestViewSHA256 != "" || value.Model.ProviderID == "" || value.Model.Model == "") {
			return errors.New("invalid recovered Memory snapshot decision")
		}
	}
	if value.ExtractEveryN < 1 {
		return errors.New("invalid Memory extraction cadence")
	}
	for _, item := range []string{value.Model.Model, value.Model.Selection, value.Model.ProviderID} {
		if len(item) > 512 || item != strings.TrimSpace(item) || strings.ContainsAny(item, "\x00\r\n") {
			return errors.New("invalid Memory model identity")
		}
	}
	if value.Model.ProfileSHA256 != "" && normalizedExtractionDigest(value.Model.ProfileSHA256) != value.Model.ProfileSHA256 {
		return errors.New("invalid Memory profile digest")
	}
	if len(value.Diagnostic) > 160 || strings.ContainsAny(value.Diagnostic, "\x00\r\n") {
		return errors.New("invalid Memory request diagnostic")
	}
	if len(value.RequestView) == 0 {
		if value.RequestViewSHA256 != "" || value.Mode == "forked" && value.Diagnostic == "" {
			return errors.New("missing Memory request view")
		}
	} else if len(value.RequestView) > MemoryExtractionViewMaxBytes || !json.Valid(value.RequestView) || extractionDigest(value.RequestView) != value.RequestViewSHA256 {
		return errors.New("invalid Memory request view digest or bounds")
	}
	if value.Model.Model == "" && value.Diagnostic == "" {
		return errors.New("missing original Memory model identity")
	}
	return nil
}

func DecodeMemoryExtractionDispatch(event OutboxEvent) (MemoryExtractionDispatch, error) {
	var value MemoryExtractionDispatch
	if event.Topic != MemoryExtractionOutboxTopic || event.Type != MemoryExtractionOutboxType || len(event.Payload) > outboxMaxPayloadBytes {
		return value, errors.New("invalid Memory delivery envelope")
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return value, errors.New("trailing Memory delivery payload")
	}
	identity, err := identifyExtractionSource(value.Source)
	if err != nil {
		return value, err
	}
	if value.Version != 1 || value.Source.Transcript == nil || value.SessionID != value.Source.FrameID || event.IdempotencyKey != identity.key ||
		event.ID != DeriveOutboxEventID(MemoryExtractionOutboxTopic, identity.key) || event.PartitionKey != memoryExtractionPartition(identity) ||
		event.AggregateType != "frame" || event.AggregateID != value.Source.FrameID || len(event.Headers) != 0 {
		return value, errors.New("Memory delivery does not match its completed source")
	}
	if value.Cadence.EveryN != value.Completion.ExtractEveryN || value.Cadence.EveryN < 1 || value.Cadence.Ordinal < 1 || value.Cadence.Eligible != (value.Cadence.Ordinal%int64(value.Cadence.EveryN) == 0) {
		return value, errors.New("invalid durable Memory cadence decision")
	}
	return value, validateMemoryExtractionCompletion(value.Completion, value.Source)
}

func MemoryExtractionClaim(event OutboxEvent) MemoryExtractionDeliveryClaim {
	return MemoryExtractionDeliveryClaim{EventID: event.ID, ClaimOwner: event.ClaimOwner, ClaimToken: event.ClaimToken, SourceKey: event.IdempotencyKey}
}

func (s *Store) CheckMemoryExtractionDeliveryClaim(ctx context.Context, event OutboxEvent) error {
	value, err := DecodeMemoryExtractionDispatch(event)
	if err != nil {
		return err
	}
	identity, err := identifyExtractionSource(value.Source)
	if err != nil {
		return err
	}
	database := s.readDB
	if database == nil {
		database = s.db
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireMemoryExtractionClaimTx(ctx, tx, MemoryExtractionClaim(event), identity.key); err != nil {
		return err
	}
	return tx.Commit()
}

func requireMemoryExtractionClaimTx(ctx context.Context, tx memoryTransaction, claim MemoryExtractionDeliveryClaim, sourceKey string) error {
	if claim.EventID != DeriveOutboxEventID(MemoryExtractionOutboxTopic, sourceKey) || claim.SourceKey != sourceKey || claim.ClaimOwner == "" || claim.ClaimToken == "" {
		return ErrOutboxClaimLost
	}
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_outbox WHERE event_id=? AND topic=? AND event_type=?
	 AND idempotency_key=? AND status='inflight' AND claim_owner=? AND claim_token=? AND lease_expires_at_ms>`+sqliteNowMillis,
		claim.EventID, MemoryExtractionOutboxTopic, MemoryExtractionOutboxType, sourceKey, claim.ClaimOwner, claim.ClaimToken).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrOutboxClaimLost
	}
	return nil
}

func settleMemoryExtractionClaimTx(ctx context.Context, tx memoryTransaction, claim MemoryExtractionDeliveryClaim, sourceKey, diagnostic string) error {
	if err := requireMemoryExtractionClaimTx(ctx, tx, claim, sourceKey); err != nil {
		return err
	}
	status, terminal := OutboxStatusDelivered, "delivered_at_ms"
	if diagnostic != "" {
		status, terminal = OutboxStatusDeadLetter, "dead_lettered_at_ms"
	}
	if len(diagnostic) > 4096 {
		diagnostic = diagnostic[:4096]
	}
	_, err := tx.ExecContext(ctx, `UPDATE workspace_outbox SET status=?,`+terminal+`=`+sqliteNowMillis+`,last_error=NULLIF(?,''),
	 claim_owner=NULL,claim_token=NULL,claimed_at_ms=NULL,lease_expires_at_ms=NULL WHERE event_id=?`, status, diagnostic, claim.EventID)
	return err
}

// SettleMemoryExtractionDelivery consumes only the current live topic claim.
// A diagnostic is a durable failed extraction, never successful Memory output.
func (s *Store) SettleMemoryExtractionDelivery(ctx context.Context, event OutboxEvent, diagnostic string) error {
	value, err := DecodeMemoryExtractionDispatch(event)
	if err != nil {
		return err
	}
	identity, err := identifyExtractionSource(value.Source)
	if err != nil {
		return err
	}
	repository, err := s.TranscriptRepository(ctx)
	if err != nil {
		return err
	}
	err = repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		return settleMemoryExtractionClaimTx(ctx, tx, MemoryExtractionClaim(event), identity.key, strings.TrimSpace(diagnostic))
	})
	if err == nil {
		s.signalOutboxWake()
	}
	return err
}
