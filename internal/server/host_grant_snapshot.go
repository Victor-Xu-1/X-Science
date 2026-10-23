package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// A revision describes the same owner-scoped snapshot returned by discovery.
// It cannot authorize I/O, resolve paths, or replace the current kernel fence.
func hostGrantSnapshotResult(userID string, grants []hostGrant) (map[string]any, error) {
	ordered := append([]hostGrant{}, grants...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	raw, err := json.Marshal(struct {
		OwnerID string      `json:"ownerId"`
		Grants  []hostGrant `json:"grants"`
	}{userID, ordered})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	items := make([]any, 0, len(ordered))
	for _, grant := range ordered {
		mode := "ro"
		if grant.Mode == "read_write" {
			mode = "rw"
		}
		item := map[string]any{"path": grant.Path, "mode": mode}
		if !grant.CreatedAt.IsZero() {
			item["grantedAt"] = grant.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
		items = append(items, item)
	}
	return map[string]any{"grants": items, "snapshotRevision": "sha256:" + hex.EncodeToString(digest[:])}, nil
}
