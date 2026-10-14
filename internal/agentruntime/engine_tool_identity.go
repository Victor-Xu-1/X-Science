package agentruntime

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// ToolCallIdentityPolicy distinguishes a bounded, in-memory conversation from
// a caller that persists tool identities beyond the provider history window.
type ToolCallIdentityPolicy string

const (
	ConversationToolCallIdentity ToolCallIdentityPolicy = "conversation"
	DurableToolCallIdentity      ToolCallIdentityPolicy = "durable"
)

type toolCallIdentityScope struct {
	prefix string
	next   uint64
}

func newToolCallIdentityScope(policy ToolCallIdentityPolicy) (*toolCallIdentityScope, error) {
	switch policy {
	case "", ConversationToolCallIdentity:
		return nil, nil
	case DurableToolCallIdentity:
		// The private scope is created for each Run, not each Engine or runner
		// attempt. An Engine can be reused by plan/correction loops, and a runner
		// attempt can survive handoff. A model-supplied ID never selects a scope.
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, fmt.Errorf("create durable tool call identity scope: %w", err)
		}
		return &toolCallIdentityScope{prefix: "call_" + hex.EncodeToString(nonce[:]) + "_"}, nil
	default:
		return nil, errors.New("unsupported tool call identity policy")
	}
}

func (scope *toolCallIdentityScope) namespace(messages []Message, calls []ToolCall, round int) ([]ToolCall, error) {
	if scope == nil {
		return namespaceReusedToolCallIDs(messages, calls, round), nil
	}
	if uint64(len(calls)) > math.MaxUint64-scope.next {
		return nil, errors.New("durable tool call identity scope is exhausted")
	}
	normalized := append([]ToolCall(nil), calls...)
	for index := range normalized {
		scope.next++
		// At most 51 ASCII bytes, including at the uint64 boundary. The raw
		// provider ID has already passed batch validation. Both the assistant
		// message and its tool result use this ID, persisted before execution.
		normalized[index].ID = scope.prefix + strconv.FormatUint(scope.next, 36)
	}
	return normalized, nil
}
