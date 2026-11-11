package server

import (
	"context"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Ordinary user body proofs are response-local. The exact event and body must
// match; a model-authored role, message ID or right-position label is no proof.
func (s *Server) transcriptWebPublicUserSources(ctx context.Context, stream transcriptstore.Stream, snapshot transcriptstore.ProjectionSnapshot, records []transcriptstore.TranscriptWebMessageRecord, messages []map[string]any) map[int]string {
	if s == nil || s.transcriptStore == nil || len(records) != len(messages) || len(records) > maxWebMessagePageSize || snapshot.StreamUID != stream.UID {
		return nil
	}
	sequences := make([]int64, 0, len(records))
	indices := make(map[int64][]int)
	for index, record := range records {
		if messages[index]["type"] != "text" || messages[index]["position"] != "right" || record.FirstPublicationSequence != record.LastPublicationSequence {
			continue
		}
		sequences = append(sequences, record.FirstPublicationSequence)
		indices[record.FirstPublicationSequence] = append(indices[record.FirstPublicationSequence], index)
	}
	if len(sequences) == 0 {
		return nil
	}
	sources, err := s.transcriptStore.ListProjectedUserSources(ctx, stream.OwnerID, snapshot, sequences)
	if err != nil {
		return nil
	}
	proved := make(map[int]string, len(sources))
	for _, source := range sources {
		payload, err := transcriptPayloadObject(source.ResolvedPayloadJSON)
		if err != nil || source.Event.StreamUID != stream.UID {
			continue
		}
		identity := strings.TrimSpace(webString(payload["messageUuid"]))
		if identity == "" {
			identity = strings.TrimSpace(webString(payload["_uuid"]))
		}
		if identity == "" {
			identity = source.Event.ClientMessageID
		}
		for _, index := range indices[source.Event.PublicationSeq] {
			message, record := messages[index], records[index]
			body, text := mapValue(message["content"])["content"].(string)
			if !text || identity == "" || record.MessageID != identity || record.ClientMessageID != source.Event.ClientMessageID ||
				message["id"] != identity || message["msg_id"] != source.Event.ClientMessageID || message["conversation_id"] != stream.SessionID ||
				body != transcriptPayloadText(payload) {
				continue
			}
			proved[index] = completionAdvisorySHA(body)
		}
	}
	return proved
}

func sanitizeTranscriptWebPublicUserMessage(message map[string]any, preservedCodes map[string]struct{}, digest string) bool {
	content, object := message["content"].(map[string]any)
	body, text := content["content"].(string)
	if digest == "" || !object || !text || message["type"] != "text" || message["position"] != "right" || completionAdvisorySHA(body) != digest {
		return false
	}
	// Only the author's body is exempt. Metadata retains the ordinary privacy
	// policy and cannot mint assistant completion observations.
	metadata := make(map[string]any, len(content)-1)
	for key, item := range content {
		if key != "content" {
			metadata[key] = item
		}
	}
	sanitizeTranscriptWebPublicValue(metadata, preservedCodes)
	delete(metadata, "completionAdvisories")
	delete(content, "completionAdvisories")
	for key, item := range metadata {
		content[key] = item
	}
	return true
}
