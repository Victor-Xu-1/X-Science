package server

import (
	"context"
	"encoding/json"
	"log"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/toolcontract"
)

const hostGrantRetainedUnknownNotice = "Directory discovery is retained outside this model context, but its freshness could not be verified. Inspect the existing retained result or report its read failure; do not repeat list_host_grants solely because its rows were externalized."

// Reuse the original immutable-result authority, not a preview, extra receipt
// or second directory cache. Retained rows never return to the model prompt.
// A bounded optional read failure is unknown, not permission or a re-list loop.
func (client runnerHostGrantContextClient) discoveryContextResult(
	ctx context.Context,
	discovery transcriptstore.ClaimedToolDiscovery,
	checkpoint map[string]any,
) (map[string]any, string) {
	result := mapValue(checkpoint["toolResult"])
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, ""
	}
	normalized, err := normalizeLegacyReusedRunnerLargeToolResultDescriptor(string(raw))
	if err != nil {
		return nil, hostGrantRetainedUnknownNotice
	}
	descriptor, _, retained, err := toolcontract.DecodeExternalizedResult([]byte(normalized))
	if !retained {
		return result, ""
	}
	if err != nil || descriptor.SizeBytes > maxSessionRunnerReadReuseEntry || !isRunnerLargeToolResultArtifactID(descriptor.ArtifactID) {
		return nil, hostGrantRetainedUnknownNotice
	}
	content, err := client.server.restoreDurableEvidencePayload(ctx, client.run.Transcript.Stream,
		sessionRunnerDurableToolCheckpoint{ToolName: listHostGrantsToolName, ToolCallID: stringValue(checkpoint["toolCallId"])},
		discovery.EventID, normalized)
	if err != nil {
		log.Printf("retained directory discovery context unavailable attempt=%d err_type=%T", client.run.Attempt, err)
		return nil, hostGrantRetainedUnknownNotice
	}
	var original map[string]any
	if json.Unmarshal([]byte(content), &original) != nil || agentruntime.ClassifyToolResult(original) != agentruntime.ToolResultSucceeded {
		return nil, hostGrantRetainedUnknownNotice
	}
	return original, "Directory discovery is retained as an immutable tool result. Read the existing result with " +
		toolcontract.CanonicalReadWith(descriptor.VersionID) +
		" before local file operations; do not repeat list_host_grants just because its rows were externalized. Runtime access checks remain authoritative."
}
