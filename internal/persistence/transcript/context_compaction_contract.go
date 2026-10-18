package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

const (
	ContextCompactionIntent         = "compact-context"
	ContextCompactionRequestSchema  = "x-science.context-compaction-request.v1"
	ContextCompactionBoundarySchema = "x-science.context-compaction-boundary.v1"
	ContextCompactionPhase          = "context_compact"
	ContextCompactionPurpose        = "context_compaction"
)

var (
	ErrContextCompactionInvalid     = errors.New("invalid context compaction request")
	ErrContextCompactionUnavailable = errors.New("context compaction target unavailable")
	ErrContextCompactionNotFound    = errors.New("context compaction request not found")
	ErrContextCompactionStale       = errors.New("context compaction scope changed")
	ErrContextCompactionBusy        = errors.New("context compaction requires an idle conversation")
	ErrContextCompactionConflict    = errors.New("context compaction request conflicts with its frozen payload")
)

type ContextCompactionTarget struct {
	FrameIncarnationID     string `json:"frame_incarnation_id"`
	RootFrameID            string `json:"root_frame_id"`
	RootFrameIncarnationID string `json:"root_frame_incarnation_id"`
	StreamUID              string `json:"stream_uid"`
	StreamEpoch            int64  `json:"stream_epoch"`
	BranchID               string `json:"branch_id"`
	BranchGeneration       int64  `json:"branch_generation"`
	InputRevision          int64  `json:"input_revision"`
	ThroughPublicationSeq  int64  `json:"through_publication_seq"`
}

type ContextCompactionRequest struct {
	Schema    string                  `json:"schema"`
	RequestID string                  `json:"request_id"`
	Expected  ContextCompactionTarget `json:"expected"`
}

type ContextCompactionReceipt struct {
	Schema               string                   `json:"schema"`
	Request              ContextCompactionRequest `json:"request"`
	FrameID              string                   `json:"frame_id"`
	Accepted             bool                     `json:"accepted"`
	InputMessageID       string                   `json:"input_message_id,omitempty"`
	InputRevision        int64                    `json:"input_revision,omitempty"`
	State                string                   `json:"state"`
	Attempt              int64                    `json:"attempt,omitempty"`
	BoundaryEventID      int64                    `json:"boundary_event_id,omitempty"`
	TerminalEventID      int64                    `json:"terminal_event_id,omitempty"`
	CancellationStatus   string                   `json:"cancellation_status"`
	ExecutionObservation string                   `json:"execution_observation"`
	CanSubmitNextInput   bool                     `json:"can_submit_next_input"`
	Replayed             bool                     `json:"replayed"`
	ArchiveStatus        string                   `json:"archive_status,omitempty"`
}

type ContextCompactionSnapshot struct {
	Schema       string                        `json:"schema"`
	Target       ContextCompactionTarget       `json:"target"`
	Availability ContextCompactionAvailability `json:"availability"`
	Operation    *ContextCompactionReceipt     `json:"operation"`
}

type ContextCompactionAvailability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

func (request ContextCompactionRequest) Validate() error {
	if request.Schema != ContextCompactionRequestSchema || request.RequestID != strings.TrimSpace(request.RequestID) {
		return ErrContextCompactionInvalid
	}
	id, err := uuid.Parse(request.RequestID)
	if err != nil || id.String() != request.RequestID {
		return ErrContextCompactionInvalid
	}
	target := request.Expected
	for _, value := range []string{target.FrameIncarnationID, target.RootFrameID, target.RootFrameIncarnationID, target.StreamUID} {
		if value == "" || len(value) > 512 || value != strings.TrimSpace(value) {
			return ErrContextCompactionInvalid
		}
	}
	if !IsValidBranchID(target.BranchID) || target.StreamEpoch <= 0 || target.BranchGeneration <= 0 || target.InputRevision < 0 || target.ThroughPublicationSeq < 0 {
		return ErrContextCompactionInvalid
	}
	return nil
}

func (request ContextCompactionRequest) MessageIntent(owner, frame string) FrameMessageIntent {
	raw, _ := json.Marshal(request)
	digest := sha256.Sum256(raw)
	return FrameMessageIntent{Version: 1, OwnerID: owner, FrameID: frame, FrameIncarnationID: request.Expected.FrameIncarnationID,
		LoadingID: request.RequestID, InputMessageID: FrameMessageIntentID(frame, request.RequestID, request.Expected.BranchID),
		TargetBranchID: request.Expected.BranchID, RequestSHA256: hex.EncodeToString(digest[:])}
}

func ContextCompactionRequestFromPayload(payload map[string]any) (*ContextCompactionRequest, error) {
	input, _ := payload["inputData"].(map[string]any)
	if input["turn_intent"] != ContextCompactionIntent {
		return nil, nil
	}
	raw, err := json.Marshal(input["context_compaction"])
	var request ContextCompactionRequest
	if err != nil || json.Unmarshal(raw, &request) != nil || request.Validate() != nil {
		return nil, ErrContextCompactionInvalid
	}
	return &request, nil
}

func HiddenConversationControlIntent(payload map[string]any) string {
	input, _ := payload["inputData"].(map[string]any)
	intent, _ := input["turn_intent"].(string)
	if intent == "save-as-skill" || intent == ContextCompactionIntent {
		return intent
	}
	return ""
}

// A payload identifies a candidate, never proof that a manual boundary committed.
func IsCompactionBoundaryPayload(payload map[string]any) bool {
	phase, _ := payload["toolPhase"].(string)
	if strings.EqualFold(strings.TrimSpace(phase), "auto_compact") {
		return true
	}
	value, _ := payload["compaction"].(map[string]any)
	return phase == ContextCompactionPhase && value["schema"] == ContextCompactionBoundarySchema
}

func IsContextCompactionTerminal(payload map[string]any) bool {
	value, _ := payload["maintenance"].(map[string]any)
	return payload["purpose"] == ContextCompactionPurpose && value["schema"] == ContextCompactionRequestSchema && value["request_id"] != ""
}

func IsContextCompactionLifecycleEvent(event Event, raw []byte) bool {
	if event.Type != "runner_checkpoint" && event.Type != "runner_finished" {
		return false
	}
	var payload map[string]any
	return json.Unmarshal(raw, &payload) == nil && payload["purpose"] == ContextCompactionPurpose
}
