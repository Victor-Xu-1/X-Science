package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	runtimekv "x-science/internal/persistence/runtimekv"
	transcriptstore "x-science/internal/persistence/transcript"
)

const notificationInboxNamespacePrefix = "notification-inbox."
const notificationInboxSchema = "x-science.notification-inbox.v1"
const notificationInboxStateSchema = "x-science.notification-inbox-state.v1"
const notificationInboxMaxBytes = 4 << 20

var errNotificationInboxConflict = errors.New("notification snapshot receipt is stale or foreign")
var errNotificationInboxCapacity = errors.New("notification snapshot capacity reached")

type notificationInboxItem struct {
	ID                  string `json:"id"`
	OriginID            string `json:"origin_id"`
	Kind                string `json:"kind"`
	ConversationID      string `json:"conversation_id"`
	FrameIncarnationID  string `json:"frame_incarnation_id"`
	Title               string `json:"title"`
	CreatedAt           string `json:"created_at"`
	ReadAt              string `json:"read_at,omitempty"`
	ActionState         string `json:"action_state,omitempty"`
	TargetInvalidatedAt string `json:"target_invalidated_at,omitempty"`
}

type notificationInboxRecord struct {
	notificationInboxItem
	ObservedRevision int64  `json:"observed_revision"`
	PendingID        string `json:"pending_id,omitempty"`
}

type notificationInboxState struct {
	Schema           string                                                `json:"schema"`
	OwnerID          string                                                `json:"owner_id"`
	Secret           string                                                `json:"secret"`
	Revision         int64                                                 `json:"revision"`
	Items            []notificationInboxRecord                             `json:"items"`
	Positions        map[string]transcriptstore.NotificationSourcePosition `json:"positions"`
	AfterSession     string                                                `json:"after_session"`
	IncludeSession   bool                                                  `json:"include_session"`
	Incomplete       bool                                                  `json:"incomplete"`
	IncompleteReason string                                                `json:"incomplete_reason,omitempty"`
	HistoryTruncated bool                                                  `json:"history_truncated"`
}

type notificationInboxSnapshot struct {
	Schema                   string                  `json:"schema"`
	HistoryScope             string                  `json:"history_scope"`
	Revision                 int64                   `json:"revision"`
	UnreadCount              int                     `json:"unread_count"`
	Items                    []notificationInboxItem `json:"items"`
	ReadReceipt              string                  `json:"read_receipt"`
	ReconciliationIncomplete bool                    `json:"reconciliation_incomplete"`
	NextCursor               string                  `json:"next_cursor,omitempty"`
	IncompleteReason         string                  `json:"incomplete_reason,omitempty"`
	HistoryTruncated         bool                    `json:"history_truncated"`
}

func notificationInboxNamespace(owner string) string {
	digest := sha256.Sum256([]byte(owner))
	return notificationInboxNamespacePrefix + hex.EncodeToString(digest[:])
}

func decodeNotificationInboxState(entry runtimekv.Entry, found bool, owner string) (notificationInboxState, error) {
	if !found {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return notificationInboxState{}, err
		}
		return notificationInboxState{Schema: notificationInboxStateSchema, OwnerID: owner,
			Secret: base64.RawURLEncoding.EncodeToString(secret), Items: []notificationInboxRecord{},
			Positions: map[string]transcriptstore.NotificationSourcePosition{}}, nil
	}
	raw, err := json.Marshal(entry.Value)
	var state notificationInboxState
	if err != nil || len(raw) > notificationInboxMaxBytes || json.Unmarshal(raw, &state) != nil ||
		state.Schema != notificationInboxStateSchema || state.OwnerID != owner || state.Revision < 1 ||
		state.Revision != int64(entry.Version) || state.Items == nil || state.Positions == nil || len(state.Positions) > 4096 || len(state.Items) > 1200 {
		return state, errors.New("invalid notification snapshot")
	}
	secret, err := base64.RawURLEncoding.DecodeString(state.Secret)
	if err != nil || len(secret) != 32 {
		return state, errors.New("invalid notification snapshot secret")
	}
	for source, position := range state.Positions {
		if source == "" || len(source) > 1024 || position.Incarnation == "" || position.Publication < 0 || position.Through < 0 ||
			(position.Through != 0 && position.Through < position.Publication) {
			return state, errors.New("invalid notification snapshot coordinate")
		}
	}
	seen := map[string]bool{}
	for _, item := range state.Items {
		if item.ID == "" || seen[item.ID] || item.OriginID == "" || item.ConversationID == "" || item.FrameIncarnationID == "" ||
			item.ID != notificationInboxID(item.OriginID) || item.ObservedRevision < 1 || item.ObservedRevision > state.Revision {
			return state, errors.New("invalid notification snapshot item")
		}
		switch item.Kind {
		case "task.completed", "task.failed", "task.needs-attention", "authorization.required":
		default:
			return state, errors.New("invalid notification snapshot kind")
		}
		if item.ActionState != "" && item.ActionState != "pending" && item.ActionState != "resolved" {
			return state, errors.New("invalid notification action state")
		}
		for _, timestamp := range []string{item.CreatedAt, item.ReadAt, item.TargetInvalidatedAt} {
			if timestamp != "" {
				if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
					return state, errors.New("invalid notification timestamp")
				}
			}
		}
		if item.CreatedAt == "" {
			return state, errors.New("missing notification timestamp")
		}
		seen[item.ID] = true
	}
	return state, nil
}

func (s *Server) loadNotificationInbox(owner string) (notificationInboxState, error) {
	entry, found, err := s.runtimeStore.Get(notificationInboxNamespace(owner), "snapshot")
	if err != nil {
		return notificationInboxState{}, err
	}
	return decodeNotificationInboxState(entry, found, owner)
}

func notificationInboxPublic(state notificationInboxState) notificationInboxSnapshot {
	result := notificationInboxSnapshot{Schema: notificationInboxSchema, HistoryScope: "active-transcript-sources", Revision: state.Revision,
		Items: make([]notificationInboxItem, 0, len(state.Items)), ReadReceipt: notificationInboxToken(state, "read", state.Revision),
		ReconciliationIncomplete: state.Incomplete, IncompleteReason: state.IncompleteReason, HistoryTruncated: state.HistoryTruncated}
	if state.Incomplete {
		result.NextCursor = notificationInboxToken(state, "cursor", state.Revision)
	}
	for _, record := range state.Items {
		result.Items = append(result.Items, record.notificationInboxItem)
		if record.ReadAt == "" {
			result.UnreadCount++
		}
	}
	sort.Slice(result.Items, func(i, j int) bool {
		return notificationInboxNewer(result.Items[i], result.Items[j])
	})
	return result
}

// Source timestamps are created from time.Time; persisted timestamps are
// validated by decodeNotificationInboxState. RFC3339Nano's optional fractional
// precision is not lexically ordered, so both display and retention compare
// actual instants and use the immutable ID only to break an equal-time tie.
func notificationInboxNewer(left, right notificationInboxItem) bool {
	leftTime, _ := time.Parse(time.RFC3339Nano, left.CreatedAt)
	rightTime, _ := time.Parse(time.RFC3339Nano, right.CreatedAt)
	if leftTime.Equal(rightTime) {
		return left.ID < right.ID
	}
	return leftTime.After(rightTime)
}

func storeNotificationInbox(entries map[string]runtimekv.Entry, state notificationInboxState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(raw) > notificationInboxMaxBytes {
		return errNotificationInboxCapacity
	}
	entries["snapshot"] = runtimekv.Entry{Key: "snapshot", Value: state, Version: int(state.Revision), UpdatedAt: time.Now().UTC()}
	return nil
}
