package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Original source evidence is ephemeral. It neither grants current package/tool
// authority nor activates a reuse key; the context owner must prove and inject it.
type runtimeSkillReplaySource struct {
	receipt                   *agentruntime.SkillLoadReceipt
	arguments, filter, prompt string
	event, acknowledgement    transcriptstore.Event
}

type runtimeSkillReplaySourceReader struct {
	server *Server
	run    *sessionRunnerChatRun
	input  transcriptstore.ListRunnerSkillLoadCheckpointsInput
	states map[int64]transcriptstore.RunnerRuntimeState
}

func (s *Server) restoreRuntimeSkillReplaySources(ctx context.Context, run *sessionRunnerChatRun, names []string) (map[string]runtimeSkillReplaySource, error) {
	if run == nil || run.Transcript == nil || run.Transcript.replaySnapshot == nil || len(names) == 0 {
		return nil, nil
	}
	current, err := s.runtimeSkillReplayBoundaryCurrent(ctx, run)
	if err != nil || !current {
		return nil, err
	}
	names = uniqueSortedFolded(names)
	if len(names) > agentruntime.MaxSkillLoadContracts {
		return nil, nil // Optional reconstruction never expands into an unbounded history walk.
	}
	authority, snapshot := run.Transcript, *run.Transcript.replaySnapshot
	reader := runtimeSkillReplaySourceReader{server: s, run: run,
		input: transcriptstore.ListRunnerSkillLoadCheckpointsInput{
			StreamUID: authority.Stream.UID, OwnerID: authority.Stream.OwnerID,
			BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
			ThroughPublicationSequence: snapshot.ThroughPublicationSequence,
			InputRevision:              authority.Claim.ClaimedInputRevision, ThroughAttempt: authority.Claim.Attempt, Limit: 32,
		}, states: make(map[int64]transcriptstore.RunnerRuntimeState)}
	sources := make(map[string]runtimeSkillReplaySource)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root := normalizedRuntimeSkillReceiptName(name)
		if root == "" {
			continue
		}
		source, err := reader.restore(ctx, root)
		if err != nil {
			if runtimeSkillReplaySourceUnavailable(err) {
				continue
			}
			return nil, err
		}
		if source.receipt != nil {
			sources[root] = source
		}
	}
	if run.Transcript == nil || run.Transcript.replaySnapshot == nil ||
		run.Transcript.Stream.UID != reader.input.StreamUID || run.Transcript.Stream.OwnerID != reader.input.OwnerID ||
		run.Transcript.Claim.Attempt != reader.input.ThroughAttempt || run.Transcript.Claim.ClaimedInputRevision != reader.input.InputRevision ||
		run.Transcript.replaySnapshot.BranchID != reader.input.BranchID ||
		run.Transcript.replaySnapshot.BranchGeneration != reader.input.BranchGeneration ||
		run.Transcript.replaySnapshot.ThroughPublicationSequence != reader.input.ThroughPublicationSequence {
		return nil, ctx.Err()
	}
	if current, err := s.runtimeSkillReplayBoundaryCurrent(ctx, run); err != nil || !current {
		return nil, err
	}
	return sources, nil
}

// Reused after materialization by the context owner. This is existing read-only
// claim authority, not a new lease/stream owner or a token persisted in receipts.
func (s *Server) runtimeSkillReplayBoundaryCurrent(ctx context.Context, run *sessionRunnerChatRun) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s == nil || s.transcriptStore == nil || run == nil || run.Transcript == nil || run.Transcript.replaySnapshot == nil {
		return false, nil
	}
	authority, frozen := run.Transcript, *run.Transcript.replaySnapshot
	if frozen.StreamUID != authority.Stream.UID || frozen.BranchID == "" || frozen.BranchGeneration <= 0 ||
		frozen.ThroughPublicationSequence <= 0 || authority.Claim.ClaimedInputRevision <= 0 {
		return false, nil
	}
	stream, err := s.transcriptStore.ValidateLiveRunnerClaim(ctx, authority.Claim)
	if err != nil {
		if runtimeSkillReplaySourceUnavailable(err) {
			return false, ctx.Err()
		}
		return false, err
	}
	if !sameLargeToolResultStream(stream, authority.Stream) || stream.InputRevision != authority.Claim.ClaimedInputRevision {
		return false, nil
	}
	latest, found, err := s.transcriptStore.GetLatestRunnerRuntimeState(ctx, stream.UID, stream.OwnerID)
	if err != nil || !found {
		return false, err
	}
	if latest.Attempt != authority.Claim.Attempt || latest.ClaimedInputRevision != authority.Claim.ClaimedInputRevision {
		return false, nil
	}
	current, err := s.transcriptStore.GetProjectionSnapshot(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		if runtimeSkillReplaySourceUnavailable(err) {
			return false, ctx.Err()
		}
		return false, err
	}
	return current.BranchID == frozen.BranchID && current.BranchGeneration == frozen.BranchGeneration &&
		current.ThroughPublicationSequence >= frozen.ThroughPublicationSequence, ctx.Err()
}

func (reader *runtimeSkillReplaySourceReader) restore(ctx context.Context, root string) (runtimeSkillReplaySource, error) {
	input := reader.input
	input.SkillName = root
	rows, err := reader.server.transcriptStore.ListRunnerSkillLoadCheckpoints(ctx, input)
	if err != nil {
		return runtimeSkillReplaySource{}, err
	}
	for _, row := range rows {
		message := runtimeSkillReplayCheckpoint(row)
		if message == nil {
			return runtimeSkillReplaySource{}, nil // A later unknown terminal cannot resurrect an older valid load.
		}
		outcome := agentruntime.ClassifyToolResult(message["toolResult"])
		if outcome.HardFailed() {
			continue
		}
		if outcome != agentruntime.ToolResultSucceeded {
			return runtimeSkillReplaySource{}, nil
		}
		ack, err := reader.prove(ctx, row, message, root)
		if err != nil || ack.receipt == nil {
			return runtimeSkillReplaySource{}, err
		}
		prompt, compact, err := reader.prompt(ctx, row, message, ack.receipt)
		if err != nil {
			return runtimeSkillReplaySource{}, err
		}
		if prompt != "" {
			ack.prompt, ack.acknowledgement = prompt, row.Event
			return ack, nil
		}
		if !compact {
			return runtimeSkillReplaySource{}, nil
		}
		input.InvocationKey, input.FullResultOnly = ack.receipt.InvocationKey, true
		originals, err := reader.server.transcriptStore.ListRunnerSkillLoadCheckpoints(ctx, input)
		if err != nil {
			return runtimeSkillReplaySource{}, err
		}
		for _, original := range originals {
			if original.Event.PublicationSeq >= row.Event.PublicationSeq {
				continue
			}
			originalMessage := runtimeSkillReplayCheckpoint(original)
			full, err := reader.prove(ctx, original, originalMessage, root)
			if err != nil {
				return runtimeSkillReplaySource{}, err
			}
			if full.receipt == nil || full.receipt.InvocationKey != ack.receipt.InvocationKey {
				continue
			}
			prompt, _, err := reader.prompt(ctx, original, originalMessage, full.receipt)
			if err != nil {
				return runtimeSkillReplaySource{}, err
			}
			if prompt != "" {
				full.prompt, full.acknowledgement = prompt, row.Event
				return full, nil
			}
		}
		return runtimeSkillReplaySource{}, nil
	}
	return runtimeSkillReplaySource{}, nil
}

func runtimeSkillReplayCheckpoint(row transcriptstore.RunnerReplayEvent) eventjournal.Message {
	if row.Event.Type != "runner_checkpoint" || row.Event.RunnerAttempt == nil {
		return nil
	}
	var message eventjournal.Message
	if json.Unmarshal(row.ResolvedPayloadJSON, &message) != nil || message == nil {
		return nil
	}
	message["type"], message["runnerAttempt"] = row.Event.Type, *row.Event.RunnerAttempt
	return message
}

func (reader *runtimeSkillReplaySourceReader) prove(ctx context.Context, row transcriptstore.RunnerReplayEvent, message eventjournal.Message, root string) (runtimeSkillReplaySource, error) {
	event, outcome := row.Event, row.ToolOutcome
	if message == nil || event.StreamUID != reader.input.StreamUID || event.EventID <= 0 ||
		event.PublicationSeq <= 0 || event.PublicationSeq > reader.input.ThroughPublicationSequence {
		return runtimeSkillReplaySource{}, nil
	}
	callID := stringValue(message["toolCallId"])
	if outcome != nil && (outcome.TerminalEventID != event.EventID || outcome.SourceEventID <= 0 ||
		outcome.SourceEventID >= event.EventID || outcome.ToolCallID != callID) {
		return runtimeSkillReplaySource{}, nil
	}
	receipt := runtimeSkillLoadReceiptFromCheckpoint(eventjournal.Entry{Message: message})
	if !runtimeSkillLoadReceiptForScope(receipt, reader.run.Transcript, callID) ||
		receipt.Binding.InputRevision != reader.input.InputRevision || receipt.Binding.RunnerAttempt > reader.input.ThroughAttempt {
		return runtimeSkillReplaySource{}, nil
	}
	input := completedSkillInput(message)
	name, nameOK := input["skill"].(string)
	args, argsOK := input["args"].(string)
	filter, filterOK := input["filter"].(string)
	_, hasArgs := input["args"]
	_, hasFilter := input["filter"]
	if !nameOK || normalizedRuntimeSkillReceiptName(name) != root || hasArgs && !argsOK || hasFilter && !filterOK ||
		!receipt.MatchesArguments(name, args, filter) {
		return runtimeSkillReplaySource{}, nil
	}
	state, err := reader.runtimeState(ctx, receipt.Binding.RunnerAttempt)
	if err != nil {
		return runtimeSkillReplaySource{}, err
	}
	if state.ClaimedInputRevision != reader.input.InputRevision || state.Attempt != receipt.Binding.RunnerAttempt {
		return runtimeSkillReplaySource{}, nil
	}
	// Batch provenance strengthens a load when present. Existing direct host
	// checkpoints retain their original durable receipt/input authority.
	if outcome != nil {
		origin, visible, err := reader.server.transcriptStore.GetProjectedEventByID(ctx, reader.projectionInput(), outcome.SourceEventID)
		if err != nil || !visible {
			return runtimeSkillReplaySource{}, err
		}
		if origin.Event.StreamUID != reader.input.StreamUID || origin.Event.Type != "runner_checkpoint" || origin.Event.RunnerAttempt == nil ||
			*origin.Event.RunnerAttempt <= 0 || *origin.Event.RunnerAttempt > receipt.Binding.RunnerAttempt ||
			origin.Event.PublicationSeq <= 0 || origin.Event.PublicationSeq >= event.PublicationSeq {
			return runtimeSkillReplaySource{}, nil
		}
		originState, err := reader.runtimeState(ctx, *origin.Event.RunnerAttempt)
		if err != nil || originState.ClaimedInputRevision != reader.input.InputRevision {
			return runtimeSkillReplaySource{}, err
		}
	}
	return runtimeSkillReplaySource{receipt: receipt, arguments: args, filter: filter, event: event}, nil
}

func (reader *runtimeSkillReplaySourceReader) runtimeState(ctx context.Context, attempt int64) (transcriptstore.RunnerRuntimeState, error) {
	if state, found := reader.states[attempt]; found {
		return state, nil
	}
	state, err := reader.server.transcriptStore.GetRunnerRuntimeState(ctx, reader.input.StreamUID, reader.input.OwnerID, attempt)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil // Absent original authority is unknown, not a task failure.
	}
	if err == nil {
		reader.states[attempt] = state
	}
	return state, err
}

func (reader *runtimeSkillReplaySourceReader) projectionInput() transcriptstore.ListProjectedEventsInput {
	return transcriptstore.ListProjectedEventsInput{StreamUID: reader.input.StreamUID, OwnerID: reader.input.OwnerID,
		BranchID: reader.input.BranchID, BranchGeneration: reader.input.BranchGeneration,
		ThroughPublicationSequence: reader.input.ThroughPublicationSequence}
}

func runtimeSkillReplaySourceUnavailable(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, transcriptstore.ErrOwnerMismatch) ||
		errors.Is(err, transcriptstore.ErrClaimStale) || errors.Is(err, transcriptstore.ErrBranchStateStale) ||
		errors.Is(err, transcriptstore.ErrEventConflict) || errors.Is(err, transcriptstore.ErrSkillLoadReplayBudget)
}

func runtimeSkillReplayCompact(value any) bool {
	result, ok := value.(map[string]any)
	return ok && result["status"] == "already_loaded" && result["reused"] == true && result["loaded"] == true &&
		strings.TrimSpace(stringValue(result["prompt"])) == ""
}
