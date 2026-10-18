package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func notificationInboxID(origin string) string {
	digest := sha256.Sum256([]byte(origin))
	return "notification-" + hex.EncodeToString(digest[:])
}

func notificationInboxTitle(value string) string {
	text := []rune(strings.TrimSpace(value))
	if len(text) > 200 {
		text = text[:200]
	}
	return string(text)
}

func notificationInboxPending(contextData map[string]any) (map[string]string, error) {
	result := map[string]string{}
	// Invalid stored state is not equivalent to an empty/resolved pending set.
	if raw, exists := contextData["_pending_input_requests"]; exists {
		values, ok := raw.([]any)
		if !ok {
			return nil, errors.New("invalid pending notification source")
		}
		for _, value := range values {
			if _, ok := value.(map[string]any); !ok {
				return nil, errors.New("invalid pending notification item")
			}
		}
	}
	for _, item := range compatibilityServerPendingInputs(contextData) {
		id := compatibilityServerPendingInputID(item)
		if id == "" || len(id) > 512 {
			return nil, errors.New("invalid pending notification identity")
		}
		if _, duplicate := result[id]; duplicate {
			return nil, errors.New("duplicate pending notification identity")
		}
		kind := "authorization.required"
		if value := strings.TrimSpace(webString(item["kind"])); value == "" || value == "ask" {
			kind = "task.needs-attention"
		}
		result[id] = kind
	}
	if len(result) > 200 {
		return nil, errNotificationInboxCapacity
	}
	return result, nil
}

func notificationInboxApplySource(state *notificationInboxState, source transcriptstore.NotificationSource) error {
	if _, exists := state.Positions[source.StreamUID]; !exists && len(state.Positions) >= 4096 {
		return errNotificationInboxCapacity
	}
	var contextData map[string]any
	if len(source.ContextJSON) > 1<<20 || json.Unmarshal(source.ContextJSON, &contextData) != nil {
		return errors.New("invalid notification source metadata")
	}
	if contextData == nil {
		contextData = map[string]any{}
	}
	pending, err := notificationInboxPending(contextData)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(state.Items))
	for _, item := range state.Items {
		known[item.ID] = true
	}
	appendItem := func(origin, kind, pendingID, created string) {
		id := notificationInboxID(origin)
		if known[id] {
			return
		}
		item := notificationInboxRecord{notificationInboxItem: notificationInboxItem{
			ID: id, OriginID: origin, Kind: kind, ConversationID: source.FrameID, FrameIncarnationID: source.Incarnation,
			Title: notificationInboxTitle(source.Name), CreatedAt: created}, ObservedRevision: state.Revision + 1, PendingID: pendingID}
		if pendingID != "" {
			item.ActionState = "pending"
		}
		state.Items = append(state.Items, item)
		known[id] = true
	}
	for _, terminal := range source.Terminals {
		kind := ""
		switch terminal.TerminalStatus {
		case "completed":
			kind = "task.completed"
		case "failed":
			kind = "task.failed"
		}
		if kind != "" {
			appendItem(fmt.Sprintf("transcript:%s:%d", source.StreamUID, terminal.EventID), kind, "", terminal.CreatedAt.UTC().Format(time.RFC3339Nano))
		}
	}
	for id, kind := range pending {
		// Pending metadata has no universal creation timestamp. This is the
		// notification's first observation time, not a fabricated tool start.
		appendItem("pending:"+source.StreamUID+":"+id, kind, id, time.Now().UTC().Format(time.RFC3339Nano))
	}
	position := transcriptstore.NotificationSourcePosition{Publication: source.Scanned, Incarnation: source.Incarnation}
	if source.Scanned < source.Through {
		position.Through = source.Through
	}
	state.Positions[source.StreamUID] = position
	return nil
}

// Refresh known pending/target facts independently of the source cursor. A full
// pending capacity can therefore recover when a question is resolved even if
// its source lies beyond the currently blocked source page.
func (s *Server) refreshNotificationInboxTargets(ctx context.Context, state *notificationInboxState) error {
	ids, seen := []string{}, map[string]bool{}
	for _, item := range state.Items {
		if !seen[item.ConversationID] {
			ids = append(ids, item.ConversationID)
			seen[item.ConversationID] = true
		}
	}
	targets, err := s.workspaceStore.NotificationTargetIncarnations(ctx, state.OwnerID, ids)
	if err != nil {
		return err
	}
	pendingByFrame := map[string]map[string]string{}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for index := range state.Items {
		item := &state.Items[index]
		if item.TargetInvalidatedAt != "" {
			continue
		}
		if targets[item.ConversationID] != item.FrameIncarnationID {
			item.TargetInvalidatedAt = now
			if item.ActionState == "pending" {
				item.ActionState = "resolved"
			}
			continue
		}
		if item.ActionState != "pending" {
			continue
		}
		pending, loaded := pendingByFrame[item.ConversationID]
		if !loaded {
			metadata, found, err := s.workspaceStore.GetFrameRuntimeMetadata(item.ConversationID)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("pending notification metadata unavailable")
			}
			pending, err = notificationInboxPending(metadata.ContextData)
			if err != nil {
				return err
			}
			pendingByFrame[item.ConversationID] = pending
		}
		if _, exists := pending[item.PendingID]; !exists {
			item.ActionState = "resolved"
		}
	}
	return nil
}

func retainNotificationInbox(state *notificationInboxState) error {
	sort.Slice(state.Items, func(i, j int) bool {
		return notificationInboxNewer(state.Items[i].notificationInboxItem, state.Items[j].notificationInboxItem)
	})
	pending, history := 0, 0
	kept := make([]notificationInboxRecord, 0, len(state.Items))
	for _, item := range state.Items {
		if item.ActionState == "pending" {
			pending++
			kept = append(kept, item)
			continue
		}
		history++
		if history <= 1000 {
			kept = append(kept, item)
		} else {
			state.HistoryTruncated = true
		}
	}
	if pending > 200 {
		return errNotificationInboxCapacity
	}
	state.Items = kept
	return nil
}
