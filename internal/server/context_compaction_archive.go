package server

import (
	"context"
	"encoding/json"
	"log"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) repairContextCompactionArchive(ctx context.Context, owner string, receipt *transcriptstore.ContextCompactionReceipt) {
	if receipt == nil || receipt.State != "committed" || receipt.ArchiveStatus == "available" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	if err := s.persistCanonicalContextCompactionArchive(ctx, owner, *receipt); err != nil {
		receipt.ArchiveStatus = "pending"
		log.Printf("context compaction archive pending frame=%q request=%q err_type=%T", receipt.FrameID, receipt.Request.RequestID, err)
		return
	}
	receipt.ArchiveStatus = "available"
}

func (s *Server) persistCanonicalContextCompactionArchive(ctx context.Context, owner string, receipt transcriptstore.ContextCompactionReceipt) error {
	boundary, generation, err := s.transcriptStore.ContextCompactionBoundary(ctx, owner, receipt.FrameID, receipt.Request)
	if err != nil {
		return err
	}
	readRequest := receipt.Request
	// Membership of this committed boundary proves the historical source
	// branch. Use the current read fence, never a different branch or cutoff.
	readRequest.Expected.BranchGeneration = generation
	entries, err := s.contextCompactionSource(ctx, readRequest, owner)
	if err != nil {
		return err
	}
	var message eventjournal.Message
	if err = json.Unmarshal(boundary.PayloadJSON, &message); err != nil {
		return err
	}
	messages := compactionMessagesAfterLastBoundary(entries)
	tokens := estimateCompactionMessageTokens(messages)
	_, err = s.workspaceStore.CreateCompactionArchive(workspace.CreateCompactionArchiveInput{
		Context: ctx,
		ID:      "context-compaction-archive:" + receipt.Request.RequestID, FrameID: receipt.FrameID, MessageCount: len(messages), TokenCount: &tokens,
		Summary: compactSummaryFromMessage(message), Messages: messages, SourceEventID: &boundary.EventID, CreatedAt: boundary.CreatedAt,
		CanonicalSource: &workspace.ContextCompactionArchiveSource{OwnerID: owner, Request: receipt.Request},
	})
	return err
}

func (s *Server) repairLatestContextCompactionArchive(frameID string) {
	if s == nil || s.workspaceStore == nil || s.transcriptStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	access, found, err := s.workspaceStore.GetFrameRealtimeContextWithContext(ctx, frameID)
	if err != nil || !found {
		return
	}
	requests, err := s.transcriptStore.PendingContextCompactionArchiveRequests(ctx, access.UserID, frameID)
	if err != nil {
		log.Printf("context compaction archive reconciliation unavailable frame=%q err_type=%T", frameID, err)
		return
	}
	for _, request := range requests {
		receipt, err := s.transcriptStore.ReadContextCompaction(ctx, access.UserID, frameID, request)
		if err != nil {
			log.Printf("context compaction archive source unavailable frame=%q err_type=%T", frameID, err)
			continue
		}
		s.repairContextCompactionArchive(ctx, access.UserID, &receipt)
	}
}
