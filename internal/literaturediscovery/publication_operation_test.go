package literaturediscovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type publicationCatalogStub struct {
	prepare func(context.Context, PublicationScope) (PublicationPreparation, error)
	commit  func(context.Context, PublicationScope, string, *PublicationSnapshot, PublicationIssue) (PublicationReceipt, error)
}

func (catalog publicationCatalogStub) PreparePublicationCheck(ctx context.Context, scope PublicationScope) (PublicationPreparation, error) {
	return catalog.prepare(ctx, scope)
}

func (catalog publicationCatalogStub) CommitPublicationCheck(ctx context.Context, scope PublicationScope, doi string, snapshot *PublicationSnapshot, issue PublicationIssue) (PublicationReceipt, error) {
	return catalog.commit(ctx, scope, doi, snapshot, issue)
}

func publicationOperationScope(operation string) PublicationScope {
	return PublicationScope{OwnerUserID: "owner", ItemID: "item", OperationID: operation, ExpectedMetadataRevision: 1}
}

func publicationOperationReceipt(scope PublicationScope, doi string, snapshot *PublicationSnapshot, issue PublicationIssue) PublicationReceipt {
	return PublicationReceipt{
		Schema:      PublicationReceiptSchema,
		OperationID: scope.OperationID,
		Result: PublicationResult{
			Schema:           PublicationResultSchema,
			ItemID:           scope.ItemID,
			MetadataRevision: scope.ExpectedMetadataRevision,
			DOI:              doi,
			Snapshot:         snapshot,
			Issue:            issue,
		},
	}
}

func publicationOperationClient(calls *atomic.Int32) ClientFor {
	return func(context.Context, string) (*http.Client, error) {
		calls.Add(1)
		return &http.Client{Transport: publicationRoundTrip(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"status":"ok","message-type":"work","message":{"DOI":"10.1234/source"}}`)),
				Request:    request,
			}, nil
		})}, nil
	}
}

func awaitPublicationSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("publication operation did not reach the controlled boundary")
	}
}

func TestPublicationRunKeepsOriginalSlotThroughCommit(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	finish := func() { unblock.Do(func() { close(release) }) }
	catalog := publicationCatalogStub{
		prepare: func(context.Context, PublicationScope) (PublicationPreparation, error) {
			return PublicationPreparation{DOI: "10.1234/source"}, nil
		},
		commit: func(ctx context.Context, scope PublicationScope, doi string, snapshot *PublicationSnapshot, issue PublicationIssue) (PublicationReceipt, error) {
			if issue == PublicationUnavailable {
				if snapshot != nil {
					return PublicationReceipt{}, errors.New("busy operation supplied replacement evidence")
				}
				return publicationOperationReceipt(scope, doi, nil, issue), nil
			}
			close(entered)
			select {
			case <-release:
				return publicationOperationReceipt(scope, doi, snapshot, issue), nil
			case <-ctx.Done():
				return PublicationReceipt{}, ctx.Err()
			}
		},
	}
	checker := NewPublicationChecker(publicationOperationClient(&calls), catalog)
	scope := publicationOperationScope("original")
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); _, err := checker.Run(context.Background(), scope); done <- err }()
	t.Cleanup(func() { finish(); awaitPublicationSignal(t, finished) })
	awaitPublicationSignal(t, entered)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := checker.Run(context.Background(), scope); !errors.Is(err, ErrPublicationPending) {
			t.Fatalf("same operation took or cleared its original slot: %v", err)
		}
	}
	other := scope
	other.OperationID = "other"
	receipt, err := checker.Run(context.Background(), other)
	if err != nil || receipt.OperationID != other.OperationID || receipt.Result.Issue != PublicationUnavailable || calls.Load() != 1 {
		t.Fatalf("conflicting operation did not commit a known busy result: %#v %v calls%d", receipt, err, calls.Load())
	}
	if _, err := checker.Run(context.Background(), scope); !errors.Is(err, ErrPublicationPending) {
		t.Fatalf("conflicting operation removed the original slot: %v", err)
	}
	finish()
	awaitPublicationSignal(t, finished)
	if err := <-done; err != nil {
		t.Fatalf("original commit failed after its slot was retained: %v", err)
	}
}

func TestPublicationRunReplaysReceiptBeforeHeldAdmission(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var saved *PublicationReceipt
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	finish := func() { unblock.Do(func() { close(release) }) }
	catalog := publicationCatalogStub{
		prepare: func(context.Context, PublicationScope) (PublicationPreparation, error) {
			mu.Lock()
			defer mu.Unlock()
			return PublicationPreparation{DOI: "10.1234/source", Replay: saved}, nil
		},
		commit: func(ctx context.Context, scope PublicationScope, doi string, snapshot *PublicationSnapshot, issue PublicationIssue) (PublicationReceipt, error) {
			receipt := publicationOperationReceipt(scope, doi, snapshot, issue)
			mu.Lock()
			saved = &receipt
			mu.Unlock()
			close(entered)
			select {
			case <-release:
				return receipt, nil
			case <-ctx.Done():
				return PublicationReceipt{}, ctx.Err()
			}
		},
	}
	checker := NewPublicationChecker(publicationOperationClient(&calls), catalog)
	scope := publicationOperationScope("original")
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); _, err := checker.Run(context.Background(), scope); done <- err }()
	t.Cleanup(func() { finish(); awaitPublicationSignal(t, finished) })
	awaitPublicationSignal(t, entered)
	receipt, err := checker.Run(context.Background(), scope)
	if err != nil || !reflect.DeepEqual(receipt, *saved) || calls.Load() != 1 {
		t.Fatalf("stored receipt lost priority over an occupied slot: %#v %v calls%d", receipt, err, calls.Load())
	}
	finish()
	awaitPublicationSignal(t, finished)
	if err := <-done; err != nil {
		t.Fatalf("original committed operation failed: %v", err)
	}
}

func TestPublicationRunReplaysCommitWithLostResponse(t *testing.T) {
	var calls atomic.Int32
	var saved *PublicationReceipt
	commits := 0
	lost := errors.New("commit response was lost")
	catalog := publicationCatalogStub{
		prepare: func(context.Context, PublicationScope) (PublicationPreparation, error) {
			return PublicationPreparation{DOI: "10.1234/source", Replay: saved}, nil
		},
		commit: func(_ context.Context, scope PublicationScope, doi string, snapshot *PublicationSnapshot, issue PublicationIssue) (PublicationReceipt, error) {
			commits++
			receipt := publicationOperationReceipt(scope, doi, snapshot, issue)
			saved = &receipt
			return PublicationReceipt{}, lost
		},
	}
	checker := NewPublicationChecker(publicationOperationClient(&calls), catalog)
	scope := publicationOperationScope("original")
	if _, err := checker.Run(context.Background(), scope); !errors.Is(err, lost) {
		t.Fatalf("unknown commit outcome was hidden: %v", err)
	}
	receipt, err := checker.Run(context.Background(), scope)
	if err != nil || !reflect.DeepEqual(receipt, *saved) || calls.Load() != 1 || commits != 1 {
		t.Fatalf("retry repeated a committed operation: %#v %v calls%d commits%d", receipt, err, calls.Load(), commits)
	}
}

func TestPublicationRunCommitsUnavailableAtGlobalCapacity(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}, 4), make(chan struct{})
	var unblock sync.Once
	finish := func() { unblock.Do(func() { close(release) }) }
	old := PublicationSnapshot{DOI: "10.1234/source", CheckedAt: time.Unix(1, 0).UTC(), Notices: []PublicationNotice{}}
	catalog := publicationCatalogStub{
		prepare: func(_ context.Context, scope PublicationScope) (PublicationPreparation, error) {
			if scope.ItemID == "missing-doi-item" {
				return PublicationPreparation{}, nil
			}
			return PublicationPreparation{DOI: "10.1234/source"}, nil
		},
		commit: func(_ context.Context, scope PublicationScope, doi string, snapshot *PublicationSnapshot, issue PublicationIssue) (PublicationReceipt, error) {
			if doi == "" && issue == PublicationDOIRequired && snapshot == nil {
				return publicationOperationReceipt(scope, doi, nil, issue), nil
			}
			if issue != PublicationUnavailable || snapshot != nil {
				return PublicationReceipt{}, errors.New("unexpected busy commit")
			}
			return publicationOperationReceipt(scope, doi, &old, issue), nil
		},
	}
	checker := NewPublicationChecker(func(ctx context.Context, _ string) (*http.Client, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return nil, errors.New("controlled request ended")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, catalog)
	done := make(chan struct{}, 4)
	scopes := []PublicationScope{
		{OwnerUserID: "owner", ItemID: "item", OperationID: "original", ExpectedMetadataRevision: 1},
		{OwnerUserID: "another-owner", ItemID: "item", OperationID: "original", ExpectedMetadataRevision: 1},
		{OwnerUserID: "owner", ItemID: "another-item", OperationID: "original", ExpectedMetadataRevision: 1},
		{OwnerUserID: "owner", ItemID: "item", OperationID: "original", ExpectedMetadataRevision: 2},
	}
	launched := 0
	t.Cleanup(func() {
		finish()
		for index := 0; index < launched; index++ {
			awaitPublicationSignal(t, done)
		}
	})
	for _, scope := range scopes {
		go func() { _, _ = checker.Run(context.Background(), scope); done <- struct{}{} }()
		launched++
		awaitPublicationSignal(t, entered)
	}
	missing := publicationOperationScope("missing-doi")
	missing.ItemID = "missing-doi-item"
	missingReceipt, err := checker.Run(context.Background(), missing)
	if err != nil || missingReceipt.Result.Issue != PublicationDOIRequired || missingReceipt.Result.DOI != "" || missingReceipt.Result.Snapshot != nil || calls.Load() != 4 {
		t.Fatalf("missing DOI borrowed network capacity or became an invalid busy receipt: %#v %v calls%d", missingReceipt, err, calls.Load())
	}
	overflow := publicationOperationScope("overflow")
	overflow.ItemID = "overflow-item"
	receipt, err := checker.Run(context.Background(), overflow)
	if err != nil || receipt.Result.Issue != PublicationUnavailable || receipt.Result.Snapshot != &old || calls.Load() != 4 {
		t.Fatalf("capacity overflow read or replaced provider evidence: %#v %v calls%d", receipt, err, calls.Load())
	}
	if _, err := checker.Run(context.Background(), scopes[0]); !errors.Is(err, ErrPublicationPending) {
		t.Fatalf("capacity overflow cleared another operation's slot: %v", err)
	}
}

func TestPublicationRunCommitsMissingDOIWithoutNetwork(t *testing.T) {
	var calls atomic.Int32
	commits := 0
	catalog := publicationCatalogStub{
		prepare: func(context.Context, PublicationScope) (PublicationPreparation, error) {
			return PublicationPreparation{}, nil
		},
		commit: func(_ context.Context, scope PublicationScope, doi string, snapshot *PublicationSnapshot, issue PublicationIssue) (PublicationReceipt, error) {
			commits++
			if doi != "" || snapshot != nil || issue != PublicationDOIRequired {
				return PublicationReceipt{}, errors.New("wrong missing DOI result")
			}
			return publicationOperationReceipt(scope, doi, nil, issue), nil
		},
	}
	checker := NewPublicationChecker(publicationOperationClient(&calls), catalog)
	receipt, err := checker.Run(context.Background(), publicationOperationScope("original"))
	if err != nil || receipt.Result.Issue != PublicationDOIRequired || commits != 1 || calls.Load() != 0 {
		t.Fatalf("missing DOI did not become a known receipt: %#v %v commits%d calls%d", receipt, err, commits, calls.Load())
	}
}

func TestPublicationRunCancellationDoesNotCommit(t *testing.T) {
	var calls atomic.Int32
	var commits atomic.Int32
	entered := make(chan struct{})
	catalog := publicationCatalogStub{
		prepare: func(context.Context, PublicationScope) (PublicationPreparation, error) {
			return PublicationPreparation{DOI: "10.1234/source"}, nil
		},
		commit: func(context.Context, PublicationScope, string, *PublicationSnapshot, PublicationIssue) (PublicationReceipt, error) {
			commits.Add(1)
			return PublicationReceipt{}, errors.New("cancelled operation reached commit")
		},
	}
	checker := NewPublicationChecker(func(ctx context.Context, _ string) (*http.Client, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}, catalog)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { _, err := checker.Run(ctx, publicationOperationScope("original")); done <- err }()
	awaitPublicationSignal(t, entered)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || calls.Load() != 1 || commits.Load() != 0 {
		t.Fatalf("cancelled read produced a receipt: %v calls%d commits%d", err, calls.Load(), commits.Load())
	}
	if _, err := checker.Run(ctx, publicationOperationScope("original")); !errors.Is(err, context.Canceled) || calls.Load() != 1 || commits.Load() != 0 {
		t.Fatalf("cancelled retry reached source or commit: %v calls%d commits%d", err, calls.Load(), commits.Load())
	}
}
