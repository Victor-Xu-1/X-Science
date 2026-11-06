package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Server) rebuildTranscriptWebFullProjection(
	ctx context.Context,
	stream transcriptstore.Stream,
	work transcriptstore.TranscriptWebProjectionWork,
) error {
	noteTranscriptWebReadStage(ctx, webReadFull)
	captured, err := s.transcriptWebFullProjectionFence(ctx, work)
	if err != nil {
		return err
	}
	if !transcriptWebFullProjectionSourceMatches(stream, work, captured) {
		return transcriptstore.ErrBranchStateStale
	}
	snapshot := transcriptstore.ProjectionSnapshot{
		StreamUID: captured.StreamUID, BranchID: captured.BranchID,
		BranchGeneration: captured.BranchGeneration, ThroughPublicationSequence: captured.ThroughPublicationSequence,
	}
	messages, err := s.projectTranscriptWebHistoryPrefix(ctx, stream, work.OwnerID, work.SessionID, snapshot)
	if err != nil {
		return fmt.Errorf("full history projection: %w", err)
	}
	return s.finishTranscriptWebFullProjection(ctx, stream, work, captured, messages, snapshot)
}

func (s *Server) transcriptWebFullProjectionFence(
	ctx context.Context,
	work transcriptstore.TranscriptWebProjectionWork,
) (transcriptstore.TranscriptWebProjectionFence, error) {
	if work.Reason == "projection_projector_version_stale" {
		return s.transcriptWebReadModel.GetTranscriptWebProjectionUpgradeFence(ctx, work.OwnerID, work.StreamUID, work.BranchID)
	}
	return s.transcriptWebReadModel.GetTranscriptWebProjectionFence(ctx, work.OwnerID, work.StreamUID, work.BranchID)
}

func transcriptWebFullProjectionSourceMatches(
	stream transcriptstore.Stream,
	work transcriptstore.TranscriptWebProjectionWork,
	captured transcriptstore.TranscriptWebProjectionFence,
) bool {
	return stream.UID == captured.StreamUID && stream.OwnerID == captured.OwnerID &&
		stream.SessionID == captured.SessionID && stream.Kind == transcriptstore.StreamKindFrameRef &&
		captured.OwnerID == work.OwnerID && captured.SessionID == work.SessionID &&
		captured.StreamUID == work.StreamUID && captured.BranchID == work.BranchID &&
		captured.BranchGeneration == work.BranchGeneration && captured.SourceRevision == work.SourceRevision &&
		captured.ThroughPublicationSequence >= work.ThroughPublicationSequence
}

func (s *Server) finishTranscriptWebFullProjection(
	ctx context.Context,
	stream transcriptstore.Stream,
	work transcriptstore.TranscriptWebProjectionWork,
	captured transcriptstore.TranscriptWebProjectionFence,
	messages []map[string]any,
	snapshot transcriptstore.ProjectionSnapshot,
) error {
	if !transcriptWebFullProjectionSourceMatches(stream, work, captured) ||
		snapshot.StreamUID != captured.StreamUID || snapshot.BranchID != captured.BranchID ||
		snapshot.BranchGeneration != captured.BranchGeneration ||
		snapshot.ThroughPublicationSequence != captured.ThroughPublicationSequence {
		return transcriptstore.ErrBranchStateStale
	}
	if _, err := s.transcriptWebFullPrefixCurrentFence(ctx, work, captured, snapshot); err != nil {
		return err
	}
	noteTranscriptWebReadStage(ctx, webReadMetadata)
	metadata, err := s.projectTranscriptWebSourceMetadata(ctx, stream, work.OwnerID, work.SessionID, snapshot)
	if err != nil {
		return fmt.Errorf("source metadata: %w", err)
	}
	noteTranscriptWebReadStage(ctx, webReadRecords)
	records, references, err := buildTranscriptWebMessageRecords(messages, metadata)
	if err != nil {
		return fmt.Errorf("build records: %w", err)
	}
	assistant, err := transcriptWebAttachIncrementalAssistantRecords(metadata.assistant, records)
	if err != nil {
		return err
	}
	fence, err := s.transcriptWebFullPrefixCurrentFence(ctx, work, captured, snapshot)
	if err != nil {
		return err
	}
	stateJSON, err := json.Marshal(transcriptWebProjectorCheckpointV1{
		Version: transcriptWebIncrementalCheckpointVersion, Mode: "full_rebuild",
		BranchGeneration:           snapshot.BranchGeneration,
		ThroughPublicationSequence: snapshot.ThroughPublicationSequence,
		SourceRevision:             fence.SourceRevision, MessageCount: len(records),
		EventChainSHA256: metadata.eventChain,
		Assistant:        assistant,
	})
	if err != nil {
		return fmt.Errorf("encode Transcript Web projector checkpoint: %w", err)
	}
	sourceChain, err := transcriptWebProjectionSourceChain(metadata.eventChain, snapshot, fence.SourceRevision, records)
	if err != nil {
		return err
	}
	projectionRevision := int64(1)
	if captured.StateFound {
		projectionRevision = captured.StateProjectionRevision + 1
	}
	status := "building"
	if fence.ThroughPublicationSequence == snapshot.ThroughPublicationSequence {
		status = "ready"
	}
	state := transcriptstore.TranscriptWebProjectionState{
		StreamUID: work.StreamUID, BranchID: work.BranchID,
		BranchGeneration: snapshot.BranchGeneration,
		ProjectorVersion: transcriptstore.TranscriptWebProjectorVersion, ProjectionRevision: projectionRevision,
		ThroughPublicationSequence: snapshot.ThroughPublicationSequence,
		SourceRevision:             fence.SourceRevision, MessageCount: len(records), VisibleMessageCount: len(records),
		MessageArtifactReferenceCount: len(references), ProjectorStateJSON: stateJSON,
		ProjectorStateSHA256: transcriptstore.TranscriptWebSHA256(stateJSON),
		SourceChainSHA256:    sourceChain, Status: status, UpdatedAt: time.Now().UTC(),
	}
	noteTranscriptWebReadStage(ctx, webReadCommit)
	err = s.transcriptWebReadModel.ApplyTranscriptWebProjection(ctx, transcriptstore.ApplyTranscriptWebProjectionInput{
		OwnerID: work.OwnerID, State: state,
		ExpectedBranchGeneration:        captured.StateBranchGeneration,
		ExpectedThroughPublication:      captured.StateThroughPublicationSequence,
		ExpectedProjectionRevision:      captured.StateProjectionRevision,
		ExpectedSourceRevision:          captured.StateSourceRevision,
		ExpectedSourceChainSHA256:       captured.StateSourceChainSHA256,
		ExpectedCanonicalSourceRevision: &captured.SourceRevision,
		ReplaceAll:                      true, Messages: records, ArtifactReferences: references,
	})
	return err
}

func (s *Server) transcriptWebFullPrefixCurrentFence(
	ctx context.Context,
	work transcriptstore.TranscriptWebProjectionWork,
	captured transcriptstore.TranscriptWebProjectionFence,
	snapshot transcriptstore.ProjectionSnapshot,
) (transcriptstore.TranscriptWebProjectionFence, error) {
	fence, err := s.transcriptWebFullProjectionFence(ctx, work)
	if err != nil {
		return fence, err
	}
	if fence.OwnerID != captured.OwnerID || fence.SessionID != captured.SessionID ||
		fence.StreamUID != snapshot.StreamUID || fence.BranchID != snapshot.BranchID ||
		fence.BranchGeneration != snapshot.BranchGeneration ||
		fence.ThroughPublicationSequence < snapshot.ThroughPublicationSequence || fence.SourceRevision != captured.SourceRevision {
		return fence, transcriptstore.ErrBranchStateStale
	}
	return fence, nil
}
