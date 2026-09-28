package server

import (
	"context"

	transcriptstore "synon-go/internal/persistence/transcript"
)

// enrichTranscriptToolArtifacts is a presentation of existing commit receipts,
// shared by live delivery and history. No model output or filename grants an
// artifact a new origin, and the persisted transcript is left untouched.
func (s *Server) enrichTranscriptToolStreamArtifacts(ctx context.Context, streamUID, ownerID string, payload map[string]any) error {
	message := map[string]any{"type": "tool_call", "content": payload["data"]}
	if err := s.enrichTranscriptToolArtifacts(ctx, streamUID, ownerID, []map[string]any{message}); err != nil {
		return err
	}
	if refs, found := message["artifact_refs"]; found {
		payload["artifact_refs"] = refs
	}
	return nil
}

func (s *Server) enrichTranscriptToolArtifacts(ctx context.Context, streamUID, ownerID string, messages []map[string]any) error {
	sources := make([]transcriptstore.ToolArtifactSource, 0)
	bySource := make(map[transcriptstore.ToolArtifactSource][]map[string]any)
	for _, message := range messages {
		if webString(message["type"]) != "tool_call" {
			continue
		}
		content, _ := message["content"].(map[string]any)
		attempt, ok := webPositiveSafeInteger(content["attempt"])
		if !ok || webString(content["call_id"]) == "" || webString(content["name"]) == "" {
			continue
		}
		source := transcriptstore.ToolArtifactSource{Attempt: attempt, CallID: webString(content["call_id"]), Name: webString(content["name"])}
		if _, seen := bySource[source]; !seen {
			sources = append(sources, source)
		}
		bySource[source] = append(bySource[source], message)
	}
	if len(sources) == 0 {
		return nil
	}
	references, err := s.transcriptStore.ToolArtifactReferences(ctx, streamUID, ownerID, sources)
	if err != nil {
		return err
	}
	for source, messages := range bySource {
		for _, message := range messages {
			if refs := references[source]; len(refs) > 0 {
				message["artifact_refs"] = transcriptArtifactReferences(refs)
			}
		}
	}
	return nil
}
