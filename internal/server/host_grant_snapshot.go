package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// The fingerprint and rows describe one original owner-scoped grant slice.
// This content identity is neither a monotonic authorization version nor I/O
// permission. Paths and modes retain their existing normalization and meaning.
func hostGrantSnapshotResult(owner string, grants []hostGrant) (map[string]any, error) {
	ordered := append([]hostGrant{}, grants...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	raw, err := json.Marshal(struct {
		Owner  string      `json:"owner"`
		Grants []hostGrant `json:"grants"`
	}{owner, ordered})
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
		items = append(items, map[string]any{"path": grant.Path, "mode": mode})
	}
	return map[string]any{"grants": items, "snapshotRevision": "sha256:" + hex.EncodeToString(digest[:])}, nil
}
