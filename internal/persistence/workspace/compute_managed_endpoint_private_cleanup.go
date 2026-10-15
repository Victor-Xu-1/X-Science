package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"x-science/internal/compute"
)

const (
	ManagedEndpointPrivateCleanupTopic = "compute.managed-endpoint-private-cleanup"
	ManagedEndpointPrivateCleanupType  = "managed_endpoint_private_cleanup"
)

type managedEndpointRetiredPrivateCleanupKey struct {
	instanceID string
	userID     string
	endpoint   string
}

// Each retired instance gets its own bounded generic outbox envelope in the
// deletion transaction. No private filesystem facts enter realtime payloads.
func (s *Store) enqueueManagedEndpointPrivateCleanupTx(ctx context.Context, tx *sql.Tx, endpoint ManagedEndpoint) error {
	var cursor string
	for {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT instance_id FROM compute_managed_endpoint_instances
			WHERE endpoint_name=? AND owner_user_id=? AND retired_at IS NOT NULL AND instance_id>?
			ORDER BY instance_id LIMIT 1`, endpoint.Name, endpoint.RegisteredBy, cursor).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		keyScope := &managedEndpointRetiredPrivateCleanupKey{instanceID: id, userID: endpoint.RegisteredBy, endpoint: endpoint.Name}
		instance, err := readManagedEndpointInstanceRowTx(ctx, tx, keyScope, "")
		if err != nil {
			return err
		}
		scope, err := compute.ManagedEndpointPrivateCleanupFor(instance)
		if err != nil {
			return err
		}
		key, err := scope.Key()
		if err != nil {
			return err
		}
		payload, err := json.Marshal(scope)
		if err != nil {
			return err
		}
		if _, err := s.EnqueueOutboxTx(ctx, tx, EnqueueOutboxInput{
			IdempotencyKey: key, Topic: ManagedEndpointPrivateCleanupTopic,
			PartitionKey: ManagedEndpointPrivateCleanupTopic + ":" + key,
			Type: ManagedEndpointPrivateCleanupType, AggregateType: "managed_endpoint_instance", AggregateID: id,
			Payload: payload, MaxAttempts: outboxDefaultMaxAttempts,
		}); err != nil {
			return err
		}
		cursor = id
	}
}
