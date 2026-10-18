package server

import (
	"context"
	"log"
	"strconv"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

// Only the original registry cleanup calls this, after closing its own done.
// The notice invalidates reads; it does not claim physical process death,
// authorize another input, or replace the canonical receipt.
func (s *Server) publishContextCompactionDrainedRun(run *activeSessionRun) {
	if s == nil || s.transcriptStore == nil || run == nil {
		return
	}
	run.settlement.Lock()
	request, authority := run.contextCompaction, run.transcript
	run.settlement.Unlock()
	if request == nil || authority == nil {
		return
	}
	ctx, stop := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer stop()
	claim := authority.Claim
	receipt, err := s.transcriptStore.ReadContextCompaction(ctx, claim.OwnerID, authority.Stream.FrameID, *request)
	if err != nil || !contextCompactionTerminalReceipt(&receipt) || receipt.Attempt != claim.Attempt ||
		receipt.InputRevision != claim.ClaimedInputRevision {
		if err != nil {
			log.Printf("context maintenance drain notice read failed err_type=%T", err)
		}
		return
	}
	_, err = s.publishCompatEventContext(ctx, workspace.RealtimeEventInput{
		ID:     "context-compaction-drained:" + request.RequestID + ":" + strconv.FormatInt(receipt.TerminalEventID, 10),
		UserID: claim.OwnerID, ProjectID: authority.Stream.ProjectID,
		RootFrameID: request.Expected.RootFrameID, FrameID: receipt.FrameID, Type: "compaction_status",
		Payload: map[string]any{
			"schema": "x-science.context-compaction-event.v1", "conversation_id": receipt.FrameID,
			"request_id": request.RequestID, "frame_incarnation_id": request.Expected.FrameIncarnationID,
			"root_frame_id": request.Expected.RootFrameID, "root_frame_incarnation_id": request.Expected.RootFrameIncarnationID,
			"state": receipt.State,
		},
	})
	if err != nil {
		log.Printf("context maintenance drain notice publication failed err_type=%T", err)
	}
}
