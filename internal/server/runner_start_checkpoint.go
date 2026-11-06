package server

import (
	"context"

	transcriptstore "x-science/internal/persistence/transcript"
)

const runnerStartedCheckpointKind = "runner_started_v1"

func (s *Server) checkpointRunnerStarted(ctx context.Context, authority *transcriptRunnerAuthority) error {
	return s.checkpointTranscriptRunner(ctx, authority, transcriptstore.RunnerPhasePlanning, "runner-started", map[string]any{
		"kind": runnerStartedCheckpointKind, "status": "running", "detail": "runner chat started",
	}, false)
}

// The typed producer marker supplies semantics. Presentation text and legacy
// unmarked checkpoints cannot establish a metadata-only lifecycle event.
func isTypedRunnerStartedCheckpoint(payload map[string]any) bool {
	if len(payload) != 3 || !mapHasOnlyKeys(payload, "kind", "status", "detail") {
		return false
	}
	kind, kindOK := payload["kind"].(string)
	status, statusOK := payload["status"].(string)
	_, detailOK := payload["detail"].(string)
	return kindOK && kind == runnerStartedCheckpointKind && statusOK && status == "running" && detailOK
}
