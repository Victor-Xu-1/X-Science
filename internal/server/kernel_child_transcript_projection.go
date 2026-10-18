package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Canonical children retain their original immutable supervision contract.
// The JSON Session is only the legacy fallback, never a second schema owner.
func (s *Server) kernelDelegateOutputSchema(ctx context.Context, session sessionstore.Session) (map[string]any, bool, error) {
	stream, authoritative, err := s.resolveTranscriptFrameStream(ctx, session.ID)
	if err != nil {
		return nil, false, err
	}
	if !authoritative {
		schema, found := kernelDelegateOutputSchemaFromSession(session)
		return schema, found, nil
	}
	if stream.Kind != transcriptstore.StreamKindFrameRef {
		return nil, false, nil
	}
	access, found, err := s.workspaceStore.GetKernelFrameAccessContext(ctx, stream.FrameID)
	if err != nil || !found {
		return nil, false, err
	}
	if access.UserID != stream.OwnerID || access.Frame.ProjectID != stream.ProjectID || access.Frame.RootFrameID != stream.RootFrameID {
		return nil, false, transcriptstore.ErrOwnerMismatch
	}
	if access.Frame.ParentFrameID == "" {
		return nil, false, nil
	}
	child, found, err := s.workspaceStore.GetKernelSupervisedChild(ctx, access.Frame.ParentFrameID, stream.FrameID, stream.OwnerID)
	if err != nil || !found {
		return nil, false, err
	}
	if child.ProjectID != stream.ProjectID || child.RootFrameID != stream.RootFrameID {
		return nil, false, transcriptstore.ErrOwnerMismatch
	}
	if len(child.OutputSchema) == 0 {
		return nil, false, nil
	}
	if err := validateKernelDelegateOutputSchema(child.OutputSchema); err != nil {
		return nil, false, errors.New("canonical delegated output schema is invalid")
	}
	return copyMapAny(child.OutputSchema), true, nil
}

// Bounded active-branch replay is already the canonical message/tool projection.
// Collect and attention detection consume it instead of an unmaintained JSONL
// shadow; a truncated/unavailable projection fails explicitly.
func (s *Server) readKernelChildEntries(ctx context.Context, frameID string) ([]eventjournal.Entry, error) {
	stream, authoritative, err := s.resolveTranscriptFrameStream(ctx, frameID)
	if err != nil {
		return nil, err
	}
	if !authoritative {
		return s.eventJournal.ReadAll(frameID)
	}
	projected, err := s.transcriptStore.ListRunnerReplay(ctx, transcriptstore.ListRunnerReplayInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, MessageLimit: 1000, CheckpointLimit: 1000,
	})
	if err != nil {
		return nil, err
	}
	entries := make([]eventjournal.Entry, 0, len(projected))
	for _, item := range projected {
		message := eventjournal.Message{}
		if err := json.Unmarshal(item.ResolvedPayloadJSON, &message); err != nil {
			return nil, err
		}
		switch item.Event.Type {
		case "user_message", "user_input_response", "history_user_message":
			message["role"] = "user"
		case "assistant_message", "history_assistant_message":
			message["role"] = "assistant"
		}
		entries = append(entries, eventjournal.Entry{SessionID: frameID, EventID: item.Event.EventID,
			CreatedAt: item.Event.CreatedAt.UTC().Format(time.RFC3339Nano), SourceEventType: item.Event.Type,
			ClientMessageID: item.Event.ClientMessageID, Message: message})
	}
	return entries, nil
}
