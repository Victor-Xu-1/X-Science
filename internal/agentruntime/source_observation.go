package agentruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// SourceEvidenceIdentity describes verified source content, never equivalence
// of the surrounding interpreter execution. Only the host receipt validator
// may construct it. Execution IDs and presentation text are deliberately absent.
type SourceEvidenceIdentity struct {
	Class, ConnectorID, ConnectorSource, ToolName  string
	InputSchemaSHA256, RequestSHA256, ResultSHA256 string
}

func (identity SourceEvidenceIdentity) Key() (string, bool) {
	if identity.Class != "bundled-readonly-source-v1" || identity.ConnectorSource != "bundled" ||
		strings.TrimSpace(identity.ConnectorID) == "" || !strings.HasPrefix(identity.ToolName, "mcp__") {
		return "", false
	}
	for _, digest := range []string{identity.InputSchemaSHA256, identity.RequestSHA256, identity.ResultSHA256} {
		raw, err := hex.DecodeString(digest)
		if err != nil || len(raw) != sha256.Size {
			return "", false
		}
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(append([]byte("x-science.source_observation.v1\x00"), raw...))
	return hex.EncodeToString(digest[:]), true
}

type SourceObservationState string

const (
	SourceObservationUnknown    SourceObservationState = "unknown"
	SourceObservationNew        SourceObservationState = "new"
	SourceObservationRepeated   SourceObservationState = "repeated"
	maxSourceObservationKeys                           = 256
	maxSourceObservationPending                        = 32
)

// SourceObservation is host-only semantic feedback. Repeated means only that
// the verified sources contributed no new content; arbitrary cell effects are
// unknown. It never authorizes caching, skipping execution, or closing a route.
type SourceObservation struct {
	State              SourceObservationState
	RepeatedOperations int
	HasSources         bool
}

type sourceObservationPending struct {
	fresh, overflow bool
	keys            map[string]struct{}
}

// SourceObservationWindow is a bounded, task-scoped projection of canonical
// receipts. A caller serializes observations in publication/execution order and
// replaces the window when owner/branch/input authority changes.
type SourceObservationWindow struct {
	seen            map[string]struct{}
	order           []string
	pending         map[string]*sourceObservationPending
	pendingOverflow bool
	repeated        int
}

func (window *SourceObservationWindow) HasPending(operation string) bool {
	return window.pending[operation] != nil
}

func (window *SourceObservationWindow) DiscardPending(operation string) {
	delete(window.pending, operation)
}

func (window *SourceObservationWindow) Unknown(operation string) {
	window.Observe(operation, "unknown")
	if group := window.pending[operation]; group != nil {
		group.overflow = true
	}
}

func (window *SourceObservationWindow) Observe(operation, key string) {
	if operation == "" || key == "" {
		return
	}
	if window.pending == nil {
		window.pending = map[string]*sourceObservationPending{}
	}
	group := window.pending[operation]
	if group == nil {
		if len(window.pending) >= maxSourceObservationPending {
			window.pendingOverflow = true
			return
		}
		group = &sourceObservationPending{keys: map[string]struct{}{}}
		window.pending[operation] = group
	}
	if len(group.keys) >= maxSourceObservationKeys {
		group.overflow = true
		return
	}
	if _, duplicate := group.keys[key]; duplicate {
		return
	}
	group.keys[key] = struct{}{}
	if window.seen == nil {
		window.seen = map[string]struct{}{}
	}
	if _, seen := window.seen[key]; seen {
		return
	}
	group.fresh = true
	window.seen[key] = struct{}{}
	window.order = append(window.order, key)
	if len(window.order) > maxSourceObservationKeys {
		delete(window.seen, window.order[0])
		window.order = window.order[1:]
	}
}

func (window *SourceObservationWindow) Complete(operation string, materialChange bool) SourceObservation {
	group := window.pending[operation]
	delete(window.pending, operation)
	observation := SourceObservation{State: SourceObservationUnknown, HasSources: group != nil || window.pendingOverflow}
	if len(window.pending) == 0 {
		window.pendingOverflow = false
	}
	if group != nil && !group.overflow && len(group.keys) > 0 {
		observation.State = SourceObservationRepeated
		if group.fresh {
			observation.State = SourceObservationNew
		}
	}
	if materialChange || observation.State != SourceObservationRepeated {
		window.repeated = 0
	} else if window.repeated < 1_000_000 {
		window.repeated++
	}
	observation.RepeatedOperations = window.repeated
	return observation
}

// A later unobserved/unknown call cannot erase earlier verified repetition.
// This aggregates source feedback, not execution effects: a new source can
// replace it, and real material progress anywhere in the batch still suppresses
// recovery feedback. Copy the completed observation so later calls cannot
// mutate a previously retained host result through an aliased pointer.
func mergeBatchSourceObservation(previous, next *SourceObservation) *SourceObservation {
	if previous != nil && previous.State == SourceObservationRepeated &&
		(next == nil || next.State == SourceObservationUnknown) {
		return previous
	}
	if next == nil {
		return nil
	}
	observation := *next
	return &observation
}

// Preserve the original tool failure while adding host-only source feedback.
// Source repetition is not execution equivalence and never closes a tool call.
func sourceObservationRecoveryFeedback(modelMessages []Message, observation *SourceObservation, batchMessages []Message, limit int, toolErr error) ([]Message, error) {
	if observation == nil || observation.State != SourceObservationRepeated || toolRoundReportsMaterialProgress(batchMessages) {
		return modelMessages, toolErr
	}
	modelMessages = append(modelMessages, Message{Role: "system", Content: sourceObservationRecoveryInstruction()})
	if toolErr == nil && limit > 0 && observation.RepeatedOperations >= limit {
		toolErr = &ToolRoundNoProgressError{Limit: limit, SourceRepetition: true}
	}
	return modelMessages, toolErr
}

func sourceObservationRecoveryInstruction() string {
	return "The latest executed operation returned repeated verified source content, not new source evidence. " +
		"Reuse the existing source receipts or choose a genuinely different needed source/action, or finish with the available evidence. " +
		"This observation does not establish that the surrounding interpreter had no other effects or computation. " +
		"Its original result is preserved; no interpreter action is closed or safe to skip on this basis."
}
