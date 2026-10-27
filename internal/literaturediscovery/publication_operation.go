package literaturediscovery

import (
	"context"
	"errors"
)

type publicationPendingKey struct {
	owner    string
	item     string
	revision int64
}

type publicationPendingOperation struct {
	id string
}

func publicationScopeKey(scope PublicationScope) publicationPendingKey {
	return publicationPendingKey{scope.OwnerUserID, scope.ItemID, scope.ExpectedMetadataRevision}
}

func (checker *PublicationChecker) reserve(scope PublicationScope) (*publicationPendingOperation, error) {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	key := publicationScopeKey(scope)
	if original := checker.pending[key]; original != nil {
		if original.id == scope.OperationID {
			return nil, ErrPublicationPending
		}
		return nil, nil
	}
	if len(checker.pending) >= 4 {
		return nil, nil
	}
	operation := &publicationPendingOperation{id: scope.OperationID}
	checker.pending[key] = operation
	return operation, nil
}

func (checker *PublicationChecker) release(scope PublicationScope, operation *publicationPendingOperation) {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	key := publicationScopeKey(scope)
	if checker.pending[key] == operation {
		delete(checker.pending, key)
	}
}

// Run owns admission through the durable commit. A lost response is retried with
// the same operation ID: Prepare may replay its receipt even while a slot exists.
func (checker *PublicationChecker) Run(ctx context.Context, scope PublicationScope) (PublicationReceipt, error) {
	var empty PublicationReceipt
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if checker.catalog == nil {
		return empty, errors.New("publication catalog is unavailable")
	}
	prepared, err := checker.catalog.PreparePublicationCheck(ctx, scope)
	if err != nil {
		return empty, err
	}
	if prepared.Replay != nil {
		return *prepared.Replay, nil
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if prepared.DOI == "" {
		return checker.commit(ctx, scope, "", nil, PublicationDOIRequired)
	}
	operation, err := checker.reserve(scope)
	if err != nil {
		return empty, err
	}
	if operation == nil {
		return checker.commit(ctx, scope, prepared.DOI, nil, PublicationUnavailable)
	}
	defer checker.release(scope, operation)
	snapshot, issue, err := checker.read(ctx, prepared.DOI)
	if err != nil {
		return empty, err
	}
	var evidence *PublicationSnapshot
	if issue == "" {
		frozen, valid := FreezePublicationSnapshot(snapshot)
		if valid {
			evidence = &frozen
		} else {
			issue = PublicationResponseInvalid
		}
	}
	return checker.commit(ctx, scope, prepared.DOI, evidence, issue)
}

func (checker *PublicationChecker) commit(ctx context.Context, scope PublicationScope, doi string, snapshot *PublicationSnapshot, issue PublicationIssue) (PublicationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return PublicationReceipt{}, err
	}
	// The catalog rechecks cancellation, owner, revision, DOI and any receipt
	// already committed under this operation inside the evidence transaction.
	return checker.catalog.CommitPublicationCheck(ctx, scope, doi, snapshot, issue)
}
