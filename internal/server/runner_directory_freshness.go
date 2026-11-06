package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"reflect"
	"strings"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
	"x-science/internal/toolcontract"
)

const (
	runnerDirectoryReadBudget = 500 * time.Millisecond
	directoryChangedNotice    = "Local directory authorization has changed. Refresh list_host_grants when current local-directory information is needed; earlier results are historical and runtime access checks remain authoritative."
	directoryMissingNotice    = "The earlier directory discovery is not present in this model request. Use list_host_grants when current local-directory information is needed; this does not grant access or block unrelated work."
	directoryUnknownNotice    = "The earlier directory discovery could not be verified against current authorization. Treat it as historical; runtime access checks still apply. Do not repeat discovery solely to satisfy this notice or because its result was externalized."
)

// This call-local projection owns no permission, cache, acknowledgement or
// retry. Copy the original claim so a late call cannot borrow a later runner.
type runnerDirectoryContext struct {
	server *Server
	claim  transcriptstore.RunnerClaim
	stream transcriptstore.Stream
}

func newRunnerDirectoryContext(server *Server, run *sessionRunnerChatRun) *runnerDirectoryContext {
	if server == nil || run == nil || run.Transcript == nil || run.frameOwnedJob || run.currentControl != nil {
		return nil
	}
	return &runnerDirectoryContext{server: server, claim: run.Transcript.Claim, stream: run.Transcript.Stream}
}

func (projection *runnerDirectoryContext) prepare(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelRequest, error) {
	if ctx != nil && ctx.Err() != nil {
		return request, ctx.Err()
	}
	if projection == nil || ctx == nil || ctx.Value(auxiliaryContextUsageKey{}) == true {
		return request, nil
	}
	available := false
	for _, tool := range request.Tools {
		available = available || tool.Name == listHostGrantsToolName
	}
	if !available {
		return request, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, runnerDirectoryReadBudget)
	defer cancel()
	discovery, err := projection.server.transcriptStore.ReadClaimedToolDiscovery(readCtx, projection.claim, listHostGrantsToolName)
	if err != nil {
		// A failed optional read cannot establish whether discovery was ever
		// observed. Do not turn unrelated tasks into a mandatory re-list loop.
		return request, directoryContextReadError(ctx, err)
	}
	input := discovery.Input
	if discovery.EventID == 0 || input == nil || input.Association == nil ||
		transcriptstore.HiddenConversationControlIntent(map[string]any{"inputData": input.InputData}) != "" {
		return request, nil
	}
	notice := directoryUnknownNotice
	result, readWith := projection.discoveryResult(readCtx, discovery)
	revision := stringValue(result["snapshotRevision"])
	if validDirectoryFingerprint(revision) && projection.server.settingsStore != nil {
		grants, readErr := projection.server.loadHostGrants(projection.claim.OwnerID)
		var current map[string]any
		if readErr == nil {
			current, readErr = hostGrantSnapshotResult(projection.claim.OwnerID, grants)
		}
		if readErr != nil {
			log.Printf("directory discovery context unavailable err_type=%T", readErr)
		} else if revision != current["snapshotRevision"] {
			notice = directoryChangedNotice
		} else if readWith != "" {
			notice = "Directory discovery is retained as an immutable tool result. Inspect it with " + readWith +
				" when local-directory information is needed; do not re-list solely because its rows were externalized. Runtime access checks remain authoritative."
		} else if directoryDiscoveryVisible(request, discovery.ToolCallID, result) {
			notice = ""
		} else {
			notice = directoryMissingNotice
		}
	}
	if err := projection.validateInput(readCtx, input); err != nil {
		return request, directoryContextReadError(ctx, err)
	}
	if notice != "" {
		request.Messages = append(append([]agentruntime.Message(nil), request.Messages...), agentruntime.Message{
			Role: "system", Content: notice, ContextUsageSource: agentruntime.ContextUsageSystemPrompt,
		})
	}
	return request, ctx.Err()
}

// Preserve original cancellation/authority errors. Optional storage timeouts or
// unavailable evidence remain unknown and do not fail the model operation.
func directoryContextReadError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, transcriptstore.ErrClaimStale) || errors.Is(err, transcriptstore.ErrOwnerMismatch) ||
		errors.Is(err, transcriptstore.ErrEventConflict) {
		return err
	}
	log.Printf("directory discovery context unavailable err_type=%T", err)
	return nil
}

func (projection *runnerDirectoryContext) validateInput(ctx context.Context, prepared *transcriptstore.ClaimedInputContext) error {
	current, err := projection.server.transcriptStore.ClaimedRunnerInputContext(ctx, projection.claim)
	if err != nil {
		return err
	}
	if current == nil || current.Association == nil || current.EventID != prepared.EventID ||
		current.PublicationSeq != prepared.PublicationSeq || current.ClientMessageID != prepared.ClientMessageID ||
		current.Association.Scope.StreamUID != projection.stream.UID || current.Association.Scope.StreamEpoch != projection.stream.Epoch {
		return transcriptstore.ErrClaimStale
	}
	a, b := *current.Association, *prepared.Association
	// New queued input may advance the read cutoff without replacing the input
	// actually admitted to this claim. All owner/branch/epoch coordinates stay.
	a.Scope.ThroughPublicationSequence, b.Scope.ThroughPublicationSequence = 0, 0
	if a != b {
		return transcriptstore.ErrClaimStale
	}
	return ctx.Err()
}

func (projection *runnerDirectoryContext) discoveryResult(ctx context.Context, discovery transcriptstore.ClaimedToolDiscovery) (map[string]any, string) {
	var checkpoint struct {
		Status                  string          `json:"status"`
		ToolName                string          `json:"toolName"`
		ToolCallID              string          `json:"toolCallId"`
		ToolPhase               string          `json:"toolPhase"`
		RejectedBeforeExecution bool            `json:"rejectedBeforeExecution"`
		Result                  json.RawMessage `json:"toolResult"`
	}
	if discovery.State != "completed" || json.Unmarshal(discovery.PayloadJSON, &checkpoint) != nil ||
		checkpoint.Status != "completed" || checkpoint.ToolPhase != "completed" || checkpoint.ToolName != listHostGrantsToolName ||
		checkpoint.ToolCallID != discovery.ToolCallID || checkpoint.RejectedBeforeExecution {
		return nil, ""
	}
	canonical, err := workspace.CanonicalToolResultContextJSON(checkpoint.Result)
	if err != nil {
		return nil, ""
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != discovery.ResultSHA256 {
		return nil, ""
	}
	normalized, err := normalizeLegacyReusedRunnerLargeToolResultDescriptor(string(canonical))
	if err != nil {
		return nil, ""
	}
	descriptor, _, retained, err := toolcontract.DecodeExternalizedResult([]byte(normalized))
	if err != nil || retained && (descriptor.SizeBytes > maxSessionRunnerReadReuseEntry || !isRunnerLargeToolResultArtifactID(descriptor.ArtifactID)) {
		return nil, ""
	}
	content, readWith := string(canonical), ""
	if retained {
		content, err = projection.server.restoreDurableEvidencePayload(ctx, projection.stream,
			sessionRunnerDurableToolCheckpoint{ToolName: listHostGrantsToolName, ToolCallID: discovery.ToolCallID},
			discovery.EventID, normalized)
		if err != nil {
			log.Printf("retained directory discovery unavailable err_type=%T", err)
			return nil, ""
		}
		readWith = toolcontract.CanonicalReadWith(descriptor.VersionID)
	}
	var result map[string]any
	if json.Unmarshal([]byte(content), &result) != nil || agentruntime.ClassifyToolResult(result) != agentruntime.ToolResultSucceeded {
		return nil, ""
	}
	return result, readWith
}

func validDirectoryFingerprint(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func directoryDiscoveryVisible(request agentruntime.ModelRequest, callID string, expected map[string]any) bool {
	if callID == "" {
		return false
	}
	for _, message := range request.Messages {
		if message.Role == "tool" && message.ToolCallID == callID {
			var result map[string]any
			if json.Unmarshal([]byte(message.Content), &result) == nil && reflect.DeepEqual(result, expected) {
				return true
			}
		}
	}
	return false
}
