package server

import (
	"context"
	"strconv"
	"strings"

	transcriptstore "synon-go/internal/persistence/transcript"
)

// enrichTranscriptWebArtifactPresentation derives one final delivery per round
// from immutable receipts. Intermediate saves remain durable but are not public
// attachment placements. It does not rewrite messages or require generating
// tools to be present in this paginated history window.
func (s *Server) enrichTranscriptWebArtifactPresentation(ctx context.Context, frameID string, messages []map[string]any) error {
	attempts := []int64{}
	for _, message := range messages {
		if webString(message["position"]) != "right" {
			message["artifact_refs"] = transcriptNonProducedArtifactReferences(message["artifact_refs"])
		}
		if attempt, ok := transcriptWebArtifactRecoveryAttempt(frameID, message); ok {
			attempts = append(attempts, attempt)
		}
	}
	if len(attempts) == 0 {
		return nil
	}
	if s == nil || s.workspaceStore == nil || s.transcriptStore == nil {
		return transcriptstore.ErrSchemaUnavailable
	}
	frame, found, err := s.workspaceStore.GetFrame(frameID)
	if err != nil || !found {
		return err
	}
	ownerID, found, err := s.workspaceStore.ProjectOwnerIDContext(ctx, frame.ProjectID)
	if err != nil || !found {
		return err
	}
	stream, found, err := s.transcriptStore.GetFrameStreamBySession(ctx, ownerID, frameID)
	if err != nil || !found {
		return err
	}
	snapshots, err := s.transcriptStore.CompletedRoundArtifactReferences(ctx, stream.UID, ownerID, attempts)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if attempt, ok := transcriptWebArtifactRecoveryAttempt(frameID, message); ok {
			message["artifact_refs"] = append(transcriptNonProducedArtifactReferences(message["artifact_refs"]),
				transcriptArtifactReferences(snapshots[attempt])...)
		}
	}
	return nil
}

func transcriptWebArtifactRecoveryAttempt(frameID string, message map[string]any) (int64, bool) {
	if strings.TrimSpace(webString(message["type"])) != "text" ||
		strings.TrimSpace(webString(message["position"])) != "left" ||
		webString(message["terminal_status"]) != "completed" || message["terminal_superseded"] == true {
		return 0, false
	}
	content, ok := message["content"].(map[string]any)
	if !ok || content == nil {
		return 0, false
	}
	identity := strings.TrimSpace(webString(content["assistant_attempt_id"]))
	if identity == "" {
		identity = strings.TrimSpace(webString(message["id"]))
	}
	prefix := "assistant-" + strings.TrimSpace(frameID) + "-"
	if !strings.HasPrefix(identity, prefix) {
		return 0, false
	}
	remainder := strings.TrimPrefix(identity, prefix)
	if segment := strings.Index(remainder, "-segment-"); segment >= 0 {
		remainder = remainder[:segment]
	}
	attempt, err := strconv.ParseInt(remainder, 10, 64)
	return attempt, err == nil && attempt > 0
}

func transcriptNonProducedArtifactReferences(value any) []map[string]any {
	refs := []map[string]any{}
	for _, ref := range transcriptWebArtifactReferenceMaps(value) {
		if webString(ref["relation"]) != "produced" {
			refs = append(refs, ref)
		}
	}
	return refs
}
