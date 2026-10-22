package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

const runtimeSkillLoadContinuityBytes = 64 << 10
const runtimeSkillLoadContinuityLimit = sessionRunnerContinuitySoftwareLimit + sessionRunnerContinuityRecentLimit +
	sessionRunnerContinuityMilestoneLimit + sessionRunnerContinuityFailureLimit

// A locator is not load authority. The immutable original event must still be
// visible in the frozen replay branch, within its original cutoff, and carry
// the exact host receipt/call/attempt/input before it can be restored.
type runtimeSkillLoadSource struct {
	EventID         int64  `json:"eventId"`
	ClientMessageID string `json:"clientMessageId"`
	ReceiptSHA256   string `json:"receiptSha256"`
}

func (source *runtimeSkillLoadSource) valid() bool {
	if source == nil || source.EventID <= 0 || source.ClientMessageID == "" || len(source.ClientMessageID) > 512 ||
		source.ClientMessageID != strings.TrimSpace(source.ClientMessageID) || len(source.ReceiptSHA256) != 64 ||
		source.ReceiptSHA256 != strings.ToLower(source.ReceiptSHA256) {
		return false
	}
	_, err := hex.DecodeString(source.ReceiptSHA256)
	return err == nil
}

func runtimeSkillLoadReceiptDigest(receipt *agentruntime.SkillLoadReceipt) string {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return ""
	}
	return runtimeSkillClosureDigest(raw)
}

func runtimeSkillLoadSourceForEntry(entry eventjournal.Entry, receipt *agentruntime.SkillLoadReceipt) *runtimeSkillLoadSource {
	if !receipt.Valid() {
		return nil
	}
	source := &runtimeSkillLoadSource{EventID: entry.EventID, ClientMessageID: entry.ClientMessageID,
		ReceiptSHA256: runtimeSkillLoadReceiptDigest(receipt)}
	if !source.valid() {
		return nil
	}
	return source
}

func boundRuntimeSkillLoadContinuity(records []sessionRunnerToolContinuityRecord) []sessionRunnerToolContinuityRecord {
	remaining := runtimeSkillLoadContinuityBytes
	for index := range records {
		source := records[index].SkillLoadSource
		if !source.valid() {
			records[index].SkillLoadSource = nil
			continue
		}
		encoded, _ := json.Marshal(source)
		remaining -= len(encoded) + len(`"skillLoadSource":`) + 2
	}
	for index := len(records) - 1; index >= 0; index-- {
		record := &records[index]
		if !record.SkillLoadSource.valid() {
			record.SkillLoadSource = nil
		}
		if record.SkillLoadReceipt != nil {
			encoded, err := json.Marshal(record.SkillLoadReceipt)
			cost := len(encoded) + len(`"skillLoadReceipt":`) + 2
			if !record.SkillLoadReceipt.Valid() || err != nil || cost > remaining {
				record.SkillLoadReceipt = nil
			} else {
				remaining -= cost
			}
		}
		// At most 51 small references remain in the original bounded ledger.
		// No original summary/name or full immutable checkpoint is discarded.
	}
	return records
}

func (s *Server) restoreReferencedSkillLoadReceipts(ctx context.Context, entries []eventjournal.Entry, run *sessionRunnerChatRun) ([]*agentruntime.SkillLoadReceipt, error) {
	if run == nil || run.Transcript == nil || run.Transcript.replaySnapshot == nil {
		return nil, nil
	}
	records, _, err := latestPersistedSessionRunnerToolContinuity(entries)
	if err != nil {
		return nil, err
	}
	hasReference := false
	for index, record := range records {
		if index >= runtimeSkillLoadContinuityLimit {
			break
		}
		hasReference = hasReference || record.SkillLoadReceipt == nil && record.SkillLoadSource.valid()
	}
	if !hasReference {
		return nil, nil
	}
	authority, snapshot := run.Transcript, *run.Transcript.replaySnapshot
	if snapshot.StreamUID != authority.Stream.UID || snapshot.BranchID == "" || snapshot.BranchGeneration <= 0 || snapshot.ThroughPublicationSequence <= 0 {
		return nil, nil
	}
	validSnapshot := func() (bool, error) {
		current, err := s.transcriptStore.GetProjectionSnapshot(ctx, authority.Stream.UID, authority.Stream.OwnerID)
		if err != nil {
			return false, err
		}
		return current.BranchID == snapshot.BranchID && current.BranchGeneration == snapshot.BranchGeneration &&
			current.ThroughPublicationSequence >= snapshot.ThroughPublicationSequence, nil
	}
	if valid, err := validSnapshot(); err != nil || !valid {
		return nil, err
	}
	// Do not substitute the newest active branch/high-water mark for the
	// already-selected replay boundary, even when newer events now exist.
	branch, err := s.transcriptStore.GetBranchProjectionSnapshot(ctx, authority.Stream.UID, authority.Stream.OwnerID, snapshot.BranchID)
	if err != nil {
		return nil, err
	}
	if branch.BranchGeneration != snapshot.BranchGeneration || branch.ThroughPublicationSequence < snapshot.ThroughPublicationSequence {
		return nil, nil
	}
	cache := make(map[string]*agentruntime.SkillLoadReceipt)
	var receipts []*agentruntime.SkillLoadReceipt
	for index, record := range records {
		if index >= runtimeSkillLoadContinuityLimit {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := record.SkillLoadSource
		if record.SkillLoadReceipt != nil || !source.valid() || !record.Successful || record.Phase != "completed" ||
			!isCompletedSkillToolName(record.ToolName) || source.EventID != record.EventID {
			continue
		}
		key := strconv.FormatInt(source.EventID, 10) + ":" + source.ClientMessageID + ":" + source.ReceiptSHA256
		receipt, checked := cache[key]
		if !checked {
			receipt, err = s.readReferencedSkillLoadReceipt(ctx, authority, snapshot, source)
			if err != nil {
				if errors.Is(err, transcriptstore.ErrBranchStateStale) {
					return nil, nil // History stays intact; normal explicit load remains available.
				}
				return nil, err
			}
			cache[key] = receipt
		}
		if runtimeSkillLoadReceiptForScope(receipt, authority, record.ToolCallID) &&
			strings.EqualFold(receipt.SkillName, record.SkillName) {
			receipts = append(receipts, receipt.Clone())
		}
	}
	if valid, err := validSnapshot(); err != nil || !valid {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return receipts, nil
}

func (s *Server) readReferencedSkillLoadReceipt(ctx context.Context, authority *transcriptRunnerAuthority, snapshot transcriptstore.ProjectionSnapshot, source *runtimeSkillLoadSource) (*agentruntime.SkillLoadReceipt, error) {
	event, found, err := s.transcriptStore.GetEventByClientMessageID(ctx, authority.Stream.UID, authority.Stream.OwnerID, source.ClientMessageID)
	if err != nil || !found {
		return nil, err
	}
	if event.EventID != source.EventID || event.Type != "runner_checkpoint" || event.RunnerAttempt == nil ||
		event.PublicationSeq <= 0 || event.PublicationSeq > snapshot.ThroughPublicationSequence {
		return nil, nil
	}
	projected, err := s.transcriptStore.ListProjectedCoordinateEvents(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: authority.Stream.UID, OwnerID: authority.Stream.OwnerID,
		BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
		AfterPublicationSequence: event.PublicationSeq - 1, ThroughPublicationSequence: event.PublicationSeq, Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(projected) != 1 || projected[0].Event.EventID != source.EventID ||
		projected[0].Event.ClientMessageID != source.ClientMessageID || projected[0].Event.RunnerAttempt == nil ||
		*projected[0].Event.RunnerAttempt != *event.RunnerAttempt {
		return nil, nil
	}
	var message eventjournal.Message
	if err := json.Unmarshal(projected[0].ResolvedPayloadJSON, &message); err != nil {
		return nil, err
	}
	message["type"], message["runnerAttempt"] = event.Type, *event.RunnerAttempt
	receipt := runtimeSkillLoadReceiptFromCheckpoint(eventjournal.Entry{Message: message})
	if !receipt.BoundToCall(stringValue(message["toolCallId"])) || runtimeSkillLoadReceiptDigest(receipt) != source.ReceiptSHA256 {
		return nil, nil
	}
	return receipt, nil
}
