package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
)

// These exact strings are historical contract facts from the pre-cutover
// implementation. They are not active product identifiers. Candidates derived
// here can only resolve a durable receipt frozen by v74; no caller writes a
// historical identity for a new effect or migrates user/artifact content.
const (
	historicalDomainRealtimeContract    = "synon-domain-realtime-v2"
	historicalRoutineRealtimeContract   = "synon-routine-realtime-v1"
	historicalConversationCloneContract = "synon-conversation-clone-v1"
)

func historicalDomainRealtimeEventID(key, eventType, aggregateID string) string {
	digest := sha256.Sum256([]byte(historicalDomainRealtimeContract + "\x00" + key + "\x00" + eventType + "\x00" + aggregateID))
	return "domain:" + hex.EncodeToString(digest[:])
}

func historicalRoutineRealtimeEventID(routine Routine, action string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s", historicalRoutineRealtimeContract, routine.ID, routine.ClaimGeneration, action)))
	return fmt.Sprintf("routine:%x", digest)
}

func historicalConversationCloneID(kind, frameID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(historicalConversationCloneContract+"\x00"+kind+"\x00"+frameID)).String()
}

func (s *Store) resolveHistoricalRealtimeIdentity(ctx context.Context, tx workspaceTransaction, canonical, historical string) (string, error) {
	return s.ResolveNamespaceIdentityTx(ctx, tx, "outbox-realtime", canonical, historical)
}
