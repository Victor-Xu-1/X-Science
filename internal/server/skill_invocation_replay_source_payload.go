package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/toolcontract"
)

// JSON escaping and normal metadata may exceed the prompt's byte size. This
// bounds hydration, never truncates a contract or weakens the existing blob cap.
const runtimeSkillReplayPayloadBytes = maxRuntimeSkillContractBytes*12 + (64 << 10)

func (reader *runtimeSkillReplaySourceReader) prompt(ctx context.Context, row transcriptstore.RunnerReplayEvent, message eventjournal.Message, receipt *agentruntime.SkillLoadReceipt) (string, bool, error) {
	value := runtimeSkillReplayPayloadValue(message["toolResult"])
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > runtimeSkillReplayPayloadBytes {
		return "", false, nil
	}
	descriptor, _, externalized, err := toolcontract.DecodeExternalizedResult(raw)
	if err != nil {
		return "", false, nil
	}
	if externalized {
		if descriptor.Outcome != string(agentruntime.ToolResultSucceeded) || descriptor.SizeBytes > runtimeSkillReplayPayloadBytes {
			return "", false, nil
		}
		content, err := reader.externalizedPrompt(ctx, row, message, receipt, descriptor, string(raw))
		if err != nil || content == "" {
			return "", false, err
		}
		value = runtimeSkillReplayPayloadValue(content)
	}
	if agentruntime.ClassifyToolResult(value) != agentruntime.ToolResultSucceeded {
		return "", false, nil
	}
	if runtimeSkillReplayCompact(value) {
		if normalizedRuntimeSkillReceiptName(stringValue(mapValue(value)["skill"])) != normalizedRuntimeSkillReceiptName(receipt.SkillName) {
			return "", false, nil
		}
		return "", true, nil
	}
	prompt, full := runtimeSkillReplayRenderedPrompt(value, receipt.SkillName)
	if !full || strings.TrimSpace(prompt) == "" || len(prompt) > maxRuntimeSkillContractBytes || !utf8.ValidString(prompt) {
		return "", false, nil
	}
	return prompt, false, ctx.Err()
}

// Decode the same structured result when a host adapter stored its JSON as a
// string. Ordinary full readable text stays byte-for-byte text, not a preview.
func runtimeSkillReplayPayloadValue(value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	var decoded any
	if json.Unmarshal([]byte(text), &decoded) == nil {
		switch decoded.(type) {
		case map[string]any, string:
			return decoded
		}
	}
	return text
}

func (reader *runtimeSkillReplaySourceReader) externalizedPrompt(ctx context.Context, row transcriptstore.RunnerReplayEvent, message eventjournal.Message, receipt *agentruntime.SkillLoadReceipt, descriptor toolcontract.ExternalizedResultDescriptor, raw string) (string, error) {
	s := reader.server
	if s.workspaceStore == nil || !isRunnerLargeToolResultArtifactID(descriptor.ArtifactID) {
		return "", nil
	}
	stream := reader.run.Transcript.Stream
	record, found, err := s.workspaceStore.GetRunnerLargeToolResult(ctx, descriptor.ArtifactID, stream.OwnerID)
	if err != nil || !found {
		return "", err
	}
	if record.StreamUID != stream.UID || record.OwnerUserID != stream.OwnerID || record.ProjectID != stream.ProjectID ||
		record.RootFrameID != stream.RootFrameID || record.FrameID != stream.FrameID || record.ArtifactID != descriptor.ArtifactID ||
		record.VersionID != descriptor.VersionID || record.ContentSHA256 != descriptor.SHA256 ||
		record.SizeBytes != descriptor.SizeBytes || record.ContentType != descriptor.ContentType ||
		record.Attempt != receipt.Binding.RunnerAttempt || record.SourceEventID <= 0 || record.SourceEventID >= row.Event.EventID ||
		record.ToolCallID != receipt.Binding.ToolCallID || record.ToolName != stringValue(message["toolName"]) ||
		record.SizeBytes > runtimeSkillReplayPayloadBytes {
		return "", nil
	}
	if state, known := reader.states[record.Attempt]; !known || state.RunnerID != record.RunnerID {
		return "", nil
	}
	// Reuse the existing source/call/tool authority, then independently require
	// that exact original source to be visible at the frozen replay coordinate.
	validated, err := s.transcriptStore.ValidateDurableRunnerToolArtifactSource(ctx, stream.UID, stream.OwnerID,
		record.SourceEventID, record.Attempt, record.ToolCallID, record.ToolName)
	if err != nil {
		if errors.Is(err, transcriptstore.ErrEventConflict) || runtimeSkillReplaySourceUnavailable(err) {
			return "", nil
		}
		return "", err
	}
	if !sameLargeToolResultStream(validated, stream) {
		return "", nil
	}
	source, visible, err := s.transcriptStore.GetProjectedEventByID(ctx, reader.projectionInput(), record.SourceEventID)
	if err != nil || !visible {
		return "", err
	}
	if source.Event.Type != "runner_checkpoint" || source.Event.RunnerAttempt == nil || *source.Event.RunnerAttempt != record.Attempt ||
		source.Event.PublicationSeq <= 0 || source.Event.PublicationSeq >= row.Event.PublicationSeq {
		return "", nil
	}
	content, err := s.restoreDurableEvidencePayload(ctx, stream, sessionRunnerDurableToolCheckpoint{
		ToolName: record.ToolName, ToolCallID: record.ToolCallID,
	}, row.Event.EventID, raw)
	if runtimeSkillReplayPayloadUnavailable(err) {
		return "", nil
	}
	return content, err
}

// A joined close/I/O error must not disappear merely because its sibling is an
// expected unavailable/conflict diagnostic from the existing payload owner.
func runtimeSkillReplayPayloadUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if !runtimeSkillReplayPayloadUnavailable(cause) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, errRunnerLargeToolResultAuthority) || errors.Is(err, errRunnerLargeToolResultConflict) ||
		errors.Is(err, errRunnerLargeToolResultUnavailable) || errors.Is(err, workspace.ErrRunnerLargeToolResultUnavailable) ||
		errors.Is(err, os.ErrNotExist)
}
