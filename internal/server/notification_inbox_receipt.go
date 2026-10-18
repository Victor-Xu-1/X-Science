package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	runtimekv "x-science/internal/persistence/runtimekv"
)

// A receipt proves only which derived snapshot was shown. It cannot authorize
// an agent action or consume an agent notification. The random per-owner key is
// retained with the snapshot across server restarts.
func notificationInboxToken(state notificationInboxState, kind string, revision int64) string {
	payload := kind + ":" + strconv.FormatInt(revision, 10)
	mac := hmac.New(sha256.New, []byte(state.Secret))
	mac.Write([]byte(state.OwnerID + "\x00" + payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func notificationInboxTokenRevision(state notificationInboxState, kind, token string) (int64, error) {
	parts := strings.Split(token, ".")
	if len(token) > 512 || len(parts) != 2 {
		return 0, errNotificationInboxConflict
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || !strings.HasPrefix(string(payload), kind+":") {
		return 0, errNotificationInboxConflict
	}
	revision, err := strconv.ParseInt(strings.TrimPrefix(string(payload), kind+":"), 10, 64)
	if err != nil || revision < 1 || revision > state.Revision || !hmac.Equal([]byte(token), []byte(notificationInboxToken(state, kind, revision))) {
		return 0, errNotificationInboxConflict
	}
	return revision, nil
}

func (s *Server) markNotificationInboxRead(owner string, ids []string, receipt string) (notificationInboxSnapshot, error) {
	var result notificationInboxSnapshot
	err := s.runtimeStore.EditNamespace(notificationInboxNamespace(owner), func(entries map[string]runtimekv.Entry) (bool, error) {
		entry, found := entries["snapshot"]
		state, err := decodeNotificationInboxState(entry, found, owner)
		if err != nil {
			return false, err
		}
		through := int64(0)
		if receipt != "" {
			through, err = notificationInboxTokenRevision(state, "read", receipt)
			if err != nil {
				return false, err
			}
		}
		selected := map[string]bool{}
		for _, id := range ids {
			selected[id] = true
		}
		// Unknown IDs are not accepted as receipts for a different owner or a
		// future source. Validate every ID before changing any read state.
		for _, item := range state.Items {
			delete(selected, item.ID)
		}
		if len(selected) != 0 {
			return false, errNotificationInboxConflict
		}
		for _, id := range ids {
			selected[id] = true
		}
		changed := false
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for index := range state.Items {
			item := &state.Items[index]
			if item.ReadAt == "" && (selected[item.ID] || (through > 0 && item.ObservedRevision <= through)) {
				item.ReadAt, changed = now, true
			}
		}
		if changed {
			state.Revision++
			if err := storeNotificationInbox(entries, state); err != nil {
				return false, err
			}
		}
		result = notificationInboxPublic(state)
		return changed, nil
	})
	return result, err
}
