package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"

	transcriptstore "x-science/internal/persistence/transcript"
)

type LiteratureTaskReference struct {
	Provider   string         `json:"provider"`
	ExternalID string         `json:"external_id"`
	Title      string         `json:"title"`
	Metadata   map[string]any `json:"metadata"`
}
type LiteratureTaskInboxRequest struct {
	OperationID string                    `json:"operation_id"`
	References  []LiteratureTaskReference `json:"references"`
}
type LiteratureTaskInboxReceipt struct {
	Schema      string                `json:"schema"`
	OperationID string                `json:"operation_id"`
	Candidates  []LiteratureCandidate `json:"candidates"`
}
type literatureTaskStoredReceipt struct {
	Scope   LiteratureTaskScope        `json:"scope"`
	Receipt LiteratureTaskInboxReceipt `json:"receipt"`
}

// All references are bounded and commit together. Pending Inbox review is a
// Library state, never an AskUser/approval wait or a runner lifecycle mutation.
func (s *Store) SaveTaskLiteratureToInbox(ctx context.Context, authority LiteratureTaskAuthority, request LiteratureTaskInboxRequest) (LiteratureTaskInboxReceipt, error) {
	var empty LiteratureTaskInboxReceipt
	id, err := uuid.Parse(request.OperationID)
	if err != nil || id.String() != request.OperationID || len(request.References) < 1 || len(request.References) > 10 {
		return empty, ErrLiteratureInvalid
	}
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > 256<<10 {
		return empty, ErrLiteratureInvalid
	}
	var frozen LiteratureTaskInboxRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&frozen); err != nil {
		return empty, ErrLiteratureInvalid
	}
	request = frozen
	// Hash exactly the caller's normalized JSON values before adding trusted
	// scope or provider enrichment; key order never changes request identity.
	requestHash, err := mutationRequestHash(request)
	if err != nil {
		return empty, err
	}
	normalized := make([]LiteratureCandidateStage, len(request.References))
	for i, ref := range request.References {
		if ref.Metadata == nil {
			return empty, ErrLiteratureInvalid
		}
		normalized[i], err = normalizeLiteratureCandidateStage(LiteratureCandidateStage{Provider: ref.Provider, ExternalID: ref.ExternalID, Title: ref.Title, Metadata: ref.Metadata})
		if err != nil {
			return empty, err
		}
	}
	var stored literatureTaskStoredReceipt
	const kind = "literature.task.save-to-inbox"
	err = transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		scope, err := literatureTaskScopeTx(ctx, tx, authority)
		if err != nil {
			return err
		}
		found, err := lookupMutationResultTx(ctx, tx, scope.OwnerID, request.OperationID, kind, requestHash, &stored)
		if err != nil {
			return err
		}
		if found {
			if stored.Scope != scope || stored.Receipt.Schema != "x-science.literature-task-inbox-receipt.v1" || stored.Receipt.OperationID != request.OperationID {
				return ErrMutationIdempotencyConflict
			}
			return nil
		}
		if err = validateLiteratureTaskAdmissionTx(ctx, tx, authority, scope); err != nil {
			return err
		}
		stored = literatureTaskStoredReceipt{Scope: scope, Receipt: LiteratureTaskInboxReceipt{Schema: "x-science.literature-task-inbox-receipt.v1", OperationID: request.OperationID, Candidates: []LiteratureCandidate{}}}
		for _, input := range normalized {
			if err = ctx.Err(); err != nil {
				return err
			}
			candidate, err := s.stageLiteratureCandidateTx(ctx, tx, scope.OwnerID, input)
			if err != nil {
				return err
			}
			if err = s.recordLiteratureTaskOriginTx(ctx, tx, scope, candidate); err != nil {
				return err
			}
			candidate, err = readLiteratureCandidate(ctx, tx, scope.OwnerID, candidate.ID)
			if err != nil {
				return err
			}
			stored.Receipt.Candidates = append(stored.Receipt.Candidates, candidate)
		}
		if err = validateLiteratureTaskAdmissionTx(ctx, tx, authority, scope); err != nil {
			return err
		}
		return insertMutationResultTx(ctx, tx, scope.OwnerID, request.OperationID, kind, requestHash, stored, s.now().UTC())
	})
	if err != nil {
		return empty, err
	}
	return stored.Receipt, nil
}
