package transcript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AppendAssistantEventWithCommittedArtifacts uses the held immediate connection
// so the assistant and its committed artifact bindings can share a terminal commit.
func (tx *ImmediateTransaction) AppendAssistantEventWithCommittedArtifacts(
	ctx context.Context,
	input AppendEventInput,
) (Event, []ArtifactReference, bool, error) {
	if tx == nil || tx.repository == nil || tx.conn == nil {
		return Event{}, nil, false, ErrSchemaUnavailable
	}
	if err := validateAssistantEventWithCommittedArtifactsInput(input); err != nil {
		return Event{}, nil, false, err
	}
	event, refs, created, err := appendAssistantEventWithCommittedArtifactsConn(ctx, tx.conn, input, tx.repository.now().UTC())
	if err != nil {
		return Event{}, nil, false, schemaError(err)
	}
	return event, refs, created, nil
}

// AppendAssistantEventWithArtifacts preserves explicit immutable references on
// the held connection. Committed bindings can then join the same transaction.
func (tx *ImmediateTransaction) AppendAssistantEventWithArtifacts(
	ctx context.Context,
	input AppendAssistantEventWithArtifactsInput,
) (Event, []ArtifactReference, bool, error) {
	if tx == nil || tx.repository == nil || tx.conn == nil {
		return Event{}, nil, false, ErrSchemaUnavailable
	}
	normalized, err := normalizeAssistantEventWithArtifactsInput(input)
	if err != nil {
		return Event{}, nil, false, err
	}
	event, refs, created, err := appendAssistantEventWithArtifactsConn(ctx, tx.conn, input, normalized, tx.repository.now().UTC())
	if err != nil {
		return Event{}, nil, false, schemaError(err)
	}
	return event, refs, created, nil
}

// CurrentArtifactCommitSnapshot reads branch-consistent artifact heads through
// the held connection, including mutations already made in this transaction.
func (tx *ImmediateTransaction) CurrentArtifactCommitSnapshot(
	ctx context.Context,
	streamUID, ownerID string,
	throughAttempt int64,
) (ArtifactCommitSnapshot, error) {
	streamUID, ownerID, err := normalizeArtifactCommitSnapshotInput(
		streamUID, ownerID, throughAttempt, tx != nil && tx.repository != nil && tx.conn != nil,
	)
	if err != nil {
		return ArtifactCommitSnapshot{}, err
	}
	return currentArtifactCommitSnapshotQuery(ctx, tx.conn, streamUID, ownerID, throughAttempt, false)
}

func validateAssistantEventWithCommittedArtifactsInput(input AppendEventInput) error {
	if input.Type != "assistant_message" {
		return errors.New("assistant_message event is required")
	}
	return validateRunnerEventInput(input)
}

func normalizeAssistantEventWithArtifactsInput(input AppendAssistantEventWithArtifactsInput) ([]ArtifactReferenceInput, error) {
	eventInput := AppendEventInput{
		Claim: input.Claim, ClientMessageID: input.ClientMessageID, Type: "assistant_message",
		Source: input.Source, PayloadJSON: input.PayloadJSON, FrameEventID: input.FrameEventID,
		Destinations: input.Destinations,
	}
	if err := validateRunnerEventInput(eventInput); err != nil {
		return nil, err
	}
	if len(input.References) == 0 || len(input.References) > maxArtifactReferencesPerAttempt {
		return nil, fmt.Errorf("1-%d artifact references are required", maxArtifactReferencesPerAttempt)
	}
	return normalizeArtifactReferenceInputs(input.References)
}

func appendAssistantEventWithCommittedArtifactsConn(
	ctx context.Context,
	conn *sql.Conn,
	input AppendEventInput,
	now time.Time,
) (Event, []ArtifactReference, bool, error) {
	var event Event
	var refs []ArtifactReference
	var created bool
	err := func() error {
		stream, err := getStreamConn(ctx, conn, input.Claim.StreamUID, input.Claim.OwnerID)
		if err != nil {
			return err
		}
		record := eventRecord{
			clientMessageID: input.ClientMessageID, eventType: input.Type, source: input.Source,
			runnerAttempt: &input.Claim.Attempt, payloadJSON: input.PayloadJSON,
			frameEventID: input.FrameEventID, destinations: input.Destinations, createdAt: now,
		}
		if existing, found, err := findEventByClientID(ctx, conn, stream.UID, input.ClientMessageID); err != nil {
			return err
		} else if found {
			if _, err := validateClaimConn(ctx, conn, input.Claim, now, false); err != nil {
				return err
			}
			if !eventMatches(existing, record) {
				return ErrEventConflict
			}
			destinations, err := deliveryDestinations(ctx, conn, existing.StreamUID, existing.PublicationSeq)
			if err != nil {
				return err
			}
			if !stringSlicesEqual(destinations, normalizedDestinations(input.Destinations)) {
				return ErrEventConflict
			}
			event = existing
		} else {
			stream, err = validateClaimConn(ctx, conn, input.Claim, now, true)
			if err != nil {
				return err
			}
			if err := validateFrameEventReferenceConn(ctx, conn, stream, input.Type, input.Source, input.FrameEventID); err != nil {
				return err
			}
			event, created, err = appendEventConn(ctx, conn, stream, record)
			if err != nil {
				return err
			}
		}
		refs, err = bindCommittedArtifactReferencesConn(ctx, conn, stream, input.Claim.Attempt, event.EventID, now)
		return err
	}()
	return event, refs, created, err
}

func appendAssistantEventWithArtifactsConn(
	ctx context.Context,
	conn *sql.Conn,
	input AppendAssistantEventWithArtifactsInput,
	normalized []ArtifactReferenceInput,
	now time.Time,
) (Event, []ArtifactReference, bool, error) {
	var event Event
	var refs []ArtifactReference
	var created bool
	err := func() error {
		stream, err := getStreamConn(ctx, conn, input.Claim.StreamUID, input.Claim.OwnerID)
		if err != nil {
			return err
		}
		if existingEvent, found, err := findEventByClientID(ctx, conn, stream.UID, input.ClientMessageID); err != nil {
			return err
		} else if found {
			if _, err := validateClaimConn(ctx, conn, input.Claim, now, false); err != nil {
				return err
			}
			record := eventRecord{
				clientMessageID: input.ClientMessageID, eventType: "assistant_message", source: input.Source,
				runnerAttempt: &input.Claim.Attempt, payloadJSON: input.PayloadJSON,
				frameEventID: input.FrameEventID, destinations: input.Destinations, createdAt: existingEvent.CreatedAt,
			}
			if !eventMatches(existingEvent, record) {
				return ErrEventConflict
			}
			destinations, err := deliveryDestinations(ctx, conn, stream.UID, existingEvent.PublicationSeq)
			if err != nil {
				return err
			}
			if !stringSlicesEqual(destinations, normalizedDestinations(input.Destinations)) {
				return ErrEventConflict
			}
			existingRefs, err := listArtifactReferencesForEventConn(ctx, conn, stream.UID, input.Claim.Attempt, existingEvent.EventID)
			if err != nil {
				return err
			}
			if !artifactReferencesMatch(existingRefs, existingEvent.EventID, normalized) {
				return ErrEventConflict
			}
			event = existingEvent
			refs = existingRefs
			return nil
		}
		stream, err = validateClaimConn(ctx, conn, input.Claim, now, true)
		if err != nil {
			return err
		}
		if err := validateFrameEventReferenceConn(ctx, conn, stream, "assistant_message", input.Source, input.FrameEventID); err != nil {
			return err
		}
		event, created, err = appendEventConn(ctx, conn, stream, eventRecord{
			clientMessageID: input.ClientMessageID, eventType: "assistant_message", source: input.Source,
			runnerAttempt: &input.Claim.Attempt, payloadJSON: input.PayloadJSON,
			frameEventID: input.FrameEventID, destinations: input.Destinations, createdAt: now,
		})
		if err != nil {
			return err
		}
		existing, err := listArtifactReferencesForEventConn(ctx, conn, stream.UID, input.Claim.Attempt, event.EventID)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			if artifactReferencesMatch(existing, event.EventID, normalized) {
				refs = existing
				return nil
			}
			return ErrEventConflict
		}
		if !created {
			return ErrEventConflict
		}
		refs, err = insertArtifactReferencesConn(ctx, conn, stream, input.Claim.Attempt, event.EventID, normalized, now)
		return err
	}()
	return event, refs, created, err
}
