package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Server) runContextCompaction(ctx context.Context, session sessionstore.Session, run *sessionRunnerChatRun, active *activeSessionRun) (bool, transcriptstore.ContextCompactionReceipt, error) {
	var receipt transcriptstore.ContextCompactionReceipt
	if run == nil || run.Transcript == nil {
		return false, receipt, nil
	}
	control, err := s.transcriptStore.ClaimedRunnerInputContext(ctx, run.Transcript.Claim)
	if err != nil {
		return false, receipt, err
	}
	if control == nil || control.InputData["turn_intent"] != transcriptstore.ContextCompactionIntent {
		return false, receipt, nil
	}
	request, err := transcriptstore.ContextCompactionRequestFromPayload(map[string]any{"inputData": control.InputData})
	if err != nil || request == nil {
		return true, receipt, transcriptstore.ErrContextCompactionInvalid
	}
	receipt, err = s.transcriptStore.ReadContextCompaction(ctx, run.Transcript.Claim.OwnerID, session.ID, *request)
	if err != nil {
		return true, receipt, err
	}
	if receipt.TerminalEventID > 0 {
		return true, receipt, nil
	}
	active.settlement.Lock()
	active.contextCompaction = request
	active.settlement.Unlock()
	start, _ := json.Marshal(map[string]any{"status": "running", "purpose": transcriptstore.ContextCompactionPurpose, "maintenance": request})
	_, _, _, err = s.transcriptStore.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: run.Transcript.Claim, ClientMessageID: transcriptRunnerClientMessageID(run.Transcript.Claim, "context-compaction-start"),
		Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: start, Destinations: []string{"ws"},
	})
	if err != nil {
		return true, receipt, err
	}
	s.signalTranscriptWebDelivery()
	entries, err := s.contextCompactionSource(ctx, *request, run.Transcript.Claim.OwnerID)
	var details map[string]any
	if err == nil {
		details, err = s.buildCanonicalCompactionDetails(session, entries, "Preserve original goals, latest corrections, verified tool outcomes, unresolved work and artifact references.", "manual")
	}
	if err == nil {
		err = ctx.Err()
	}
	active.settlement.Lock()
	if err == nil {
		details["status"], details["toolPhase"], details["purpose"] = "completed", transcriptstore.ContextCompactionPhase, transcriptstore.ContextCompactionPurpose
		details["maintenance"] = request
		details["compaction"] = map[string]any{"schema": transcriptstore.ContextCompactionBoundarySchema, "reason": "manual", "request_id": request.RequestID,
			"source_branch_id": request.Expected.BranchID, "source_through_publication_seq": request.Expected.ThroughPublicationSeq}
		receipt, err = s.transcriptStore.FinishContextCompaction(ctx, run.Transcript.Claim, *request, details)
	}
	if err != nil {
		log.Printf("context maintenance did not commit frame=%q request=%q err_type=%T", session.ID, request.RequestID, err)
		settlementCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if errors.Is(err, transcriptstore.ErrContextCompactionStale) {
			receipt, err = s.workspaceStore.CancelContextCompaction(settlementCtx, run.Transcript.Claim.OwnerID, session.ID, *request)
		} else {
			receipt, err = s.transcriptStore.FailContextCompaction(settlementCtx, run.Transcript.Claim, *request)
		}
		cancel()
	}
	if err == nil && (receipt.TerminalEventID > 0 || receipt.State == "cancelled") {
		active.settled = true
	}
	active.settlement.Unlock()
	s.signalTranscriptWebDelivery()
	if err != nil {
		return true, receipt, err
	}
	return true, receipt, nil
}
