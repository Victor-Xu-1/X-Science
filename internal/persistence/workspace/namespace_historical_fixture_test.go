package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Reconstructs the actual published opaque ID for a controlled old DB fixture.
// Production reuses these receipts by natural keys, not a legacy write path.
func historicalOutboxEventID(topic, key string) string {
	digest := sha256.Sum256([]byte("synon-outbox-v1\x00" + strings.TrimSpace(topic) + "\x00" + strings.TrimSpace(key)))
	return "evt_" + hex.EncodeToString(digest[:])
}
