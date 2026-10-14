package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/toolcontract"
)

type runnerSourceObservationProjection struct {
	scope, branchID         string
	branchGeneration, after int64
	reducer                 runnerSourceObservationReducer
}

// A transient witness is created only after the normal immutable payload
// validator succeeds. It is never accepted from model or persisted JSON.
type runnerSourceObservationWitness struct {
	key                  string
	proposalEventID      int64
	executionResult      any
	executionUnavailable bool
}

func (s *Server) validateRunnerSourceObservationEntry(ctx context.Context, authority *transcriptRunnerAuthority, entry eventjournal.Entry, snapshots ...transcriptstore.ProjectionSnapshot) (eventjournal.Entry, error) {
	if entry.SourceEventType != "runner_checkpoint" {
		return entry, nil
	}
	schema := stringValue(entry.Message["schema"])
	sourceReceipt := schema == "x-science.kernel_mcp_evidence.v1" || schema == workspaceMCPSourceEvidenceSchemaV1
	if !sourceReceipt && !isAgentKernelToolName(stringValue(entry.Message["toolName"])) {
		return entry, nil
	}
	if entry.Message["status"] != "completed" || entry.Message["toolPhase"] != "completed" {
		return entry, nil
	}
	entry.RuntimeProjection = runnerSourceObservationWitness{}
	if s == nil || authority == nil || s.workspaceStore == nil || s.transcriptStore == nil {
		return entry, nil
	}
	raw, err := json.Marshal(entry.Message)
	if err != nil {
		return entry, err
	}
	var checkpoint sessionRunnerDurableToolCheckpoint
	if json.Unmarshal(raw, &checkpoint) != nil || sourceReceipt && !validSessionRunnerMCPDurableCheckpoint(checkpoint) {
		return entry, nil
	}
	var proposal int64
	if schema == "x-science.kernel_mcp_evidence.v1" {
		operation, found, err := s.workspaceStore.GetKernelLocalOperation(ctx, authority.Stream.OwnerID, checkpoint.KernelOperationID)
		if err != nil {
			return entry, err
		}
		if !found || operation.StreamUID != authority.Stream.UID || operation.ToolCallID != checkpoint.OuterToolCallID || operation.ExecutionID != checkpoint.ExecutionID || operation.KernelID != checkpoint.KernelID || operation.KernelGeneration != checkpoint.KernelGeneration {
			return entry, nil
		}
		proposal = operation.SourceEventID
	} else {
		var snapshot transcriptstore.ProjectionSnapshot
		if len(snapshots) > 0 {
			snapshot = snapshots[0]
		} else {
			var err error
			snapshot, err = s.transcriptStore.GetProjectionSnapshot(ctx, authority.Stream.UID, authority.Stream.OwnerID)
			if err != nil {
				return entry, err
			}
		}
		var found bool
		proposal, found, err = s.workspaceStore.ToolCallProposalForTerminalReceipt(ctx, authority.Stream.OwnerID, checkpoint.ToolCallID, checkpoint.ToolName, entry.EventID, snapshot)
		if err != nil {
			return entry, err
		}
		if !found {
			proposal = 0
		}
	}
	content, restoreErr := s.restoreDurableEvidencePayload(ctx, authority.Stream, checkpoint, entry.EventID, string(checkpoint.ToolResult))
	if restoreErr != nil {
		if ctx.Err() != nil {
			return entry, ctx.Err()
		}
		slog.WarnContext(ctx, "runner_source_observation_unavailable", "reason", "source_content_unverified")
		entry.RuntimeProjection = runnerSourceObservationWitness{proposalEventID: proposal, executionUnavailable: !sourceReceipt}
		return entry, nil
	}
	if !sourceReceipt {
		var result any
		if json.Unmarshal([]byte(content), &result) == nil {
			entry.RuntimeProjection = runnerSourceObservationWitness{proposalEventID: proposal, executionResult: result}
		}
		return entry, nil
	}
	if descriptor, _, externalized, err := toolcontract.DecodeExternalizedResult(checkpoint.ToolResult); externalized {
		if err != nil {
			return entry, nil
		}
		key, valid := runnerVerifiedSourceIdentity(entry.Message, descriptor.SHA256)
		if valid {
			entry.RuntimeProjection = runnerSourceObservationWitness{key: key, proposalEventID: proposal}
		}
	} else if key, valid := runnerVerifiedSourceIdentity(entry.Message); valid {
		entry.RuntimeProjection = runnerSourceObservationWitness{key: key, proposalEventID: proposal}
	}
	return entry, nil
}

// This is a bounded derived projection over the existing canonical branch,
// not another receipt authority. It scans only newly published events while a
// runner lives; restart/branch/input changes reconstruct the exact same facts.
func (s *Server) runnerToolSourceObservation(ctx context.Context, run *sessionRunnerChatRun, call agentruntime.ToolCall, value any) *agentruntime.SourceObservation {
	if s == nil || s.transcriptStore == nil || run == nil || run.Transcript == nil {
		return nil
	}
	run.sourceObservationMu.Lock()
	defer run.sourceObservationMu.Unlock()
	observation, err := s.projectRunnerToolSourceObservation(ctx, run, call, value)
	if err != nil {
		// Observation failure must not misreport or repeat an already executed
		// operation. Drop derived state rather than retaining a partial scan.
		unknown := agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown}
		run.sourceObservations = nil
		run.sourceObservationResultCallID, run.sourceObservationResult = call.ID, &unknown
		slog.WarnContext(ctx, "runner_source_observation_unavailable", "reason", "canonical_projection_failed")
		return &unknown
	}
	run.sourceObservationResultCallID, run.sourceObservationResult = call.ID, observation
	return observation
}

// Engine emits the terminal lifecycle synchronously before consuming this
// host-only pointer. Direct MCP evidence first becomes authoritative there.
func (s *Server) refreshRunnerToolSourceObservation(ctx context.Context, run *sessionRunnerChatRun, call agentruntime.ToolCall, value any) {
	if run == nil {
		return
	}
	run.sourceObservationMu.Lock()
	defer run.sourceObservationMu.Unlock()
	if run.sourceObservationResultCallID != call.ID || run.sourceObservationResult == nil {
		return
	}
	observation, err := s.projectRunnerToolSourceObservation(ctx, run, call, value)
	if err != nil {
		run.sourceObservations = nil
		*run.sourceObservationResult = agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown}
		slog.WarnContext(ctx, "runner_source_observation_unavailable", "reason", "terminal_projection_failed")
		return
	}
	*run.sourceObservationResult = *observation
}

func (s *Server) projectRunnerToolSourceObservation(ctx context.Context, run *sessionRunnerChatRun, call agentruntime.ToolCall, value any) (*agentruntime.SourceObservation, error) {
	authority := run.Transcript
	current, err := s.transcriptStore.GetStream(ctx, authority.Stream.UID, authority.Stream.OwnerID)
	if err != nil {
		return nil, err
	}
	if current.Epoch != authority.Stream.Epoch || current.InputRevision != authority.Claim.ClaimedInputRevision {
		return nil, fmt.Errorf("source observation input authority changed")
	}
	snapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, authority.Stream.UID, authority.Stream.OwnerID)
	if err != nil {
		return nil, err
	}
	scope := runnerRecoveryObligationFingerprint(authority, transcriptstore.RunnerInterruptionCause{})
	projection := run.sourceObservations
	if projection == nil || projection.scope != scope || projection.branchID != snapshot.BranchID || projection.branchGeneration != snapshot.BranchGeneration {
		projection = &runnerSourceObservationProjection{scope: scope, branchID: snapshot.BranchID, branchGeneration: snapshot.BranchGeneration}
		boundary, err := s.runnerSourceObservationInputBoundary(ctx, authority, snapshot)
		if err != nil {
			return nil, err
		}
		projection.after = boundary
		if boundary > 0 {
			projection.reducer = runnerSourceObservationReducer{inputScoped: true, admitted: map[string]int64{}}
		}
		run.sourceObservations = projection
	}
	if !projection.reducer.inputScoped {
		return &agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown}, nil
	}
	const pageSize = 128
	for projection.after < snapshot.ThroughPublicationSequence {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := s.transcriptStore.ListProjectedCoordinateEvents(ctx, transcriptstore.ListProjectedEventsInput{
			StreamUID: authority.Stream.UID, OwnerID: authority.Stream.OwnerID,
			BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
			AfterPublicationSequence: projection.after, ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: pageSize,
		})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return nil, fmt.Errorf("source observation projection ended before snapshot")
		}
		for _, projected := range page {
			projection.after = projected.Event.PublicationSeq
			if projected.Event.Type != "runner_checkpoint" && projected.Event.Type != "user_message" && projected.Event.Type != "user_input_response" {
				continue
			}
			var message eventjournal.Message
			if err := json.Unmarshal(projected.ResolvedPayloadJSON, &message); err != nil {
				return nil, err
			}
			message["type"] = "runner_checkpoint"
			entry, err := s.validateRunnerSourceObservationEntry(ctx, authority, eventjournal.Entry{EventID: projected.Event.EventID, Message: message, SourceEventType: projected.Event.Type}, snapshot)
			if err != nil {
				return nil, err
			}
			projection.reducer.observeEntry(entry)
		}
	}
	current, err = s.transcriptStore.GetStream(ctx, authority.Stream.UID, authority.Stream.OwnerID)
	if err != nil {
		return nil, err
	}
	finalSnapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, authority.Stream.UID, authority.Stream.OwnerID)
	if err != nil {
		return nil, err
	}
	if current.Epoch != authority.Stream.Epoch || current.InputRevision != authority.Claim.ClaimedInputRevision || finalSnapshot.BranchID != snapshot.BranchID || finalSnapshot.BranchGeneration != snapshot.BranchGeneration {
		return nil, fmt.Errorf("source observation projection authority changed")
	}
	if !projection.reducer.inputScoped {
		return &agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown}, nil
	}
	return projection.reducer.completeLive(call, value), nil
}
