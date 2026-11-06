package server

import (
	"encoding/hex"
	"encoding/json"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Metadata checkpoints advance the canonical source chain without creating a
// message or changing assistant/tool boundaries. All incremental consumers use
// this same predicate; unsupported checkpoint shapes still require full replay.
func transcriptWebIncrementalMetadataCheckpoint(projected transcriptstore.ProjectedEvent) bool {
	if transcriptWebIncrementalProviderBoundary(projected) {
		return true
	}
	if projected.Event.Type != "runner_checkpoint" || projected.Event.Source != transcriptstore.EventSourcePayload ||
		projected.Event.RunnerAttempt == nil || *projected.Event.RunnerAttempt <= 0 ||
		projected.Event.FrameEventID != nil || len(projected.ArtifactReferences) != 0 {
		return false
	}
	const prefix = "transcript-runner:"
	identity := strings.TrimPrefix(projected.Event.ClientMessageID, prefix)
	if identity == projected.Event.ClientMessageID || len(identity) != 64 || strings.ToLower(identity) != identity {
		return false
	}
	if _, err := hex.DecodeString(identity); err != nil {
		return false
	}
	var payload map[string]any
	if json.Unmarshal(projected.ResolvedPayloadJSON, &payload) != nil {
		return false
	}
	if isTypedRunnerStartedCheckpoint(payload) {
		return true
	}
	if len(payload) != 4 || !mapHasOnlyKeys(payload, "status", "stage", "detail", "lifecyclePhase") {
		return false
	}
	status, statusOK := payload["status"].(string)
	stage, stageOK := payload["stage"].(string)
	detail, detailOK := payload["detail"].(string)
	lifecycle, lifecycleOK := payload["lifecyclePhase"].(string)
	if !statusOK || !stageOK || !detailOK || !lifecycleOK || status != "running" || strings.TrimSpace(stage) != stage {
		return false
	}
	// Reuse the actual producer's stage contract instead of accepting arbitrary
	// running checkpoints or maintaining a second stage/detail/phase table.
	expectedDetail := sessionRunnerPreparationStageDetail(stage)
	if expectedDetail == sessionRunnerPreparationStageDetail("") || detail != expectedDetail ||
		lifecycle != string(sessionRunnerPreparationPhase(stage)) {
		return false
	}
	// Durable resume_state can rebase a legacy assistant segment after a tool.
	// Its full-replay boundary is meaningful even though it has no own message.
	return !transcriptWebRunnerResumeStateFence(payload)
}
