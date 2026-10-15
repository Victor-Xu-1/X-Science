package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

// One bounded delivery batch in the existing owner supervisor, not a separate
// scheduler. A filesystem failure is durably retried/dead-lettered, never a
// reason to prevent unrelated endpoint actions from being reconciled.
func (s *Server) reconcileManagedEndpointPrivateCleanup(ctx context.Context) {
	round, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	events, err := s.workspaceStore.ClaimOutbox(round, workspace.ClaimOutboxInput{
		WorkerID: "managed-endpoint-private-cleanup-" + uuid.NewString(),
		Topics: []string{workspace.ManagedEndpointPrivateCleanupTopic}, Limit: 8, Lease: 30 * time.Second,
	})
	if err != nil {
		if round.Err() == nil {
			log.Printf("managed endpoint private cleanup claim: %v", err)
		}
		return
	}
	for _, event := range events {
		if round.Err() != nil {
			return
		}
		if err := s.deliverManagedEndpointPrivateCleanup(round, event); err != nil {
			_, err = s.workspaceStore.RetryOutbox(round, event.ID, event.ClaimToken, err.Error(), time.Second)
			if err != nil && round.Err() == nil {
				log.Printf("managed endpoint private cleanup retry: %v", err)
			}
		} else if err := s.workspaceStore.AckOutbox(round, event.ID, event.ClaimToken); err != nil && round.Err() == nil {
			log.Printf("managed endpoint private cleanup ack: %v", err)
		}
	}
}

func (s *Server) deliverManagedEndpointPrivateCleanup(ctx context.Context, event workspace.OutboxEvent) error {
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	var scope compute.ManagedEndpointPrivateCleanup
	if err := decoder.Decode(&scope); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("managed endpoint private cleanup payload has trailing data")
	}
	key, err := scope.Key()
	if err != nil {
		return err
	}
	if event.Topic != workspace.ManagedEndpointPrivateCleanupTopic || event.Type != workspace.ManagedEndpointPrivateCleanupType ||
		event.IdempotencyKey != key || event.ID != workspace.DeriveOutboxEventID(event.Topic, key) ||
		event.PartitionKey != event.Topic+":"+key || event.AggregateType != "managed_endpoint_instance" || event.AggregateID != scope.InstanceID {
		return errors.New("managed endpoint private cleanup envelope does not match its scope")
	}
	return compute.CleanupManagedEndpointPrivateFiles(ctx, filepath.Join(s.fileRoot+"-managed-endpoints", ".owners"), scope)
}
