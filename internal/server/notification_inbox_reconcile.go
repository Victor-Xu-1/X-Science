package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	runtimekv "x-science/internal/persistence/runtimekv"
)

// Reconciliation is initiated only by this independent read path. In particular
// no runner, delivery ACK, outbox partition, or text frontier waits on it.
func (s *Server) reconcileNotificationInbox(ctx context.Context, owner, cursor string) (notificationInboxSnapshot, error) {
	state, err := s.loadNotificationInbox(owner)
	if err != nil {
		return notificationInboxSnapshot{}, err
	}
	if cursor != "" {
		revision, err := notificationInboxTokenRevision(state, "cursor", cursor)
		if err != nil || revision != state.Revision || !state.Incomplete {
			return notificationInboxSnapshot{}, errNotificationInboxConflict
		}
	}
	baseRevision := state.Revision
	// Source I/O is outside the runtime state transaction. Publication progress
	// and read state commit together only if the original snapshot still matches.
	err = s.refreshNotificationInboxTargets(ctx, &state)
	if err == nil {
		var candidate notificationInboxState
		raw, _ := json.Marshal(state)
		_ = json.Unmarshal(raw, &candidate)
		page, sourceErr := s.transcriptStore.ReadNotificationSources(ctx, owner, state.AfterSession, state.IncludeSession, state.Positions)
		if sourceErr == nil {
			for _, source := range page.Sources {
				if sourceErr = notificationInboxApplySource(&candidate, source); sourceErr != nil {
					break
				}
			}
		}
		if sourceErr == nil {
			sourceErr = retainNotificationInbox(&candidate)
		}
		if sourceErr == nil {
			candidate.AfterSession, candidate.IncludeSession = page.AfterSession, page.IncludeSession
			candidate.Incomplete, candidate.IncompleteReason = !page.Complete, ""
			if candidate.Incomplete {
				candidate.IncompleteReason = "more_sources"
			}
			encoded, encodeErr := json.Marshal(candidate)
			if encodeErr != nil {
				sourceErr = encodeErr
			} else if len(encoded) > notificationInboxMaxBytes-notificationInboxMutationReserve(candidate) {
				sourceErr = errNotificationInboxCapacity
			}
		}
		if sourceErr == nil {
			state = candidate
		} else {
			err = sourceErr
		}
	}
	if err != nil {
		state.Incomplete, state.IncompleteReason = true, "source_unavailable"
		if errors.Is(err, errNotificationInboxCapacity) {
			state.IncompleteReason = "capacity"
		}
	}
	var result notificationInboxSnapshot
	err = s.runtimeStore.EditNamespace(notificationInboxNamespace(owner), func(entries map[string]runtimekv.Entry) (bool, error) {
		entry, found := entries["snapshot"]
		current, err := decodeNotificationInboxState(entry, found, owner)
		if err != nil {
			return false, err
		}
		if current.Revision != baseRevision {
			return false, errNotificationInboxConflict
		}
		if found && reflect.DeepEqual(current, state) {
			result = notificationInboxPublic(current)
			return false, nil
		}
		state.Revision = baseRevision + 1
		if err := storeNotificationInbox(entries, state); err != nil {
			return false, err
		}
		result = notificationInboxPublic(state)
		return true, nil
	})
	return result, err
}

// Every newly accepted snapshot must leave room for read and invalidated-target
// timestamps on every record. A fixed 4 KiB reserve covers neither a 1000-item
// read-all nor later target invalidation. RFC3339Nano timestamps are at most 35
// bytes; 128 bytes per item covers both optional fields, separators and the
// pending-to-resolved label change. The fixed margin covers snapshot metadata.
func notificationInboxMutationReserve(state notificationInboxState) int {
	return 4096 + 128*len(state.Items)
}
