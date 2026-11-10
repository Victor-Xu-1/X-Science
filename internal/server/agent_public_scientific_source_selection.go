package server

import (
	"context"
	"encoding/json"

	transcriptstore "x-science/internal/persistence/transcript"
)

const nativeScientificSourceSelectionKind = "selection"
const nativeScientificSourceSelectionPhase = "source_selection_completed"

// Selection associates an already-real source with this parent. It never
// claims that a second web_fetch or model tool call executed.
type nativeScientificSourceSelection struct {
	nativeScientificSourceParent
	Schema              string `json:"schema"`
	Kind                string `json:"kind"`
	ToolPhase           string `json:"toolPhase"`
	SelectionID         string `json:"selectionId"`
	SourceToolCallID    string `json:"sourceToolCallId"`
	SourceEventID       int64  `json:"sourceEventId"`
	SourcePayloadSHA256 string `json:"sourcePayloadSha256"`
}

func (selection nativeScientificSourceSelection) identity() string {
	raw, _ := json.Marshal([]any{agentPublicScientificAcquisitionSchema, nativeScientificSourceSelectionKind,
		selection.StreamUID, selection.OwnerID, selection.BranchID, selection.BranchGeneration,
		selection.ParentBatchID, selection.ParentOrdinal, selection.ParentToolCallID, selection.ParentSourceEventID,
		selection.ParentArgumentsSHA256, selection.ParentExecutedSHA256, selection.SourceURL,
		selection.SourceToolCallID, selection.SourceEventID, selection.SourcePayloadSHA256})
	return "download-source-selection-" + scientificSourceDigest(raw)[:32]
}

func (s *Server) recordNativeScientificSourceSelection(ctx context.Context, run transcriptArtifactRun, parentID string, input map[string]any, request agentPublicScientificFileRequest, binding agentPublicScientificSourceBinding) error {
	// Registered execution catalog assets retain their existing independent
	// immutable catalog proof. They do not invent a Transcript source event.
	if binding.EventID == 0 {
		return nil
	}
	task, _ := transcriptRunnerChatRunFromContext(ctx)
	parent, err := s.newNativeScientificSourceParent(ctx, task, run, parentID, input, false)
	if err != nil {
		return err
	}
	snapshot, err := s.validateNativeScientificSourceParent(ctx, parent, true, false)
	if err != nil {
		return err
	}
	source, found, err := s.transcriptStore.GetProjectedEventByID(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: parent.StreamUID, OwnerID: parent.OwnerID, BranchID: snapshot.BranchID,
		BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence,
	}, binding.EventID)
	if err != nil || !found || scientificSourceDigest(agentPublicScientificSourceEventPayload(source)) != binding.PayloadSHA256 {
		return errAgentPublicScientificFileSource
	}
	verified, matched, err := s.agentPublicScientificCompletedSourceBinding(ctx, parent.OwnerID, snapshot, source, request)
	if err != nil || !matched || verified.ToolCallID != binding.ToolCallID {
		return errAgentPublicScientificFileSource
	}
	var acquisition nativeScientificAcquisition
	if json.Unmarshal(agentPublicScientificSourceEventPayload(source), &acquisition) == nil &&
		acquisition.Schema == agentPublicScientificAcquisitionSchema && acquisition.Kind == "" &&
		acquisition.ParentSourceEventID == parent.ParentSourceEventID && acquisition.ParentToolCallID == parent.ParentToolCallID &&
		acquisition.ParentExecutedSHA256 == parent.ParentExecutedSHA256 {
		// This exact parent's real acquisition completion already records the
		// selection, so do not append a redundant private fact.
		return nil
	}
	selection := nativeScientificSourceSelection{nativeScientificSourceParent: parent,
		Schema: agentPublicScientificAcquisitionSchema, Kind: nativeScientificSourceSelectionKind,
		ToolPhase: nativeScientificSourceSelectionPhase, SourceToolCallID: binding.ToolCallID,
		SourceEventID: binding.EventID, SourcePayloadSHA256: binding.PayloadSHA256}
	selection.SelectionID = selection.identity()
	return s.appendNativeScientificSourceFact(ctx, run, parent, selection.SelectionID, selection, false)
}

func (s *Server) validateNativeScientificSourceSelection(ctx context.Context, snapshot transcriptstore.ProjectionSnapshot, event transcriptstore.ProjectedEvent, request agentPublicScientificFileRequest) (agentPublicScientificSourceBinding, error) {
	var selection nativeScientificSourceSelection
	if json.Unmarshal(agentPublicScientificSourceEventPayload(event), &selection) != nil ||
		selection.Schema != agentPublicScientificAcquisitionSchema || selection.Kind != nativeScientificSourceSelectionKind ||
		selection.ToolPhase != nativeScientificSourceSelectionPhase || selection.SelectionID != selection.identity() ||
		selection.SourceURL != request.SourceURL || selection.SourceToolCallID == "" || selection.SourceEventID <= 0 ||
		!validSHA256Hex(selection.SourcePayloadSHA256) || selection.StreamUID != snapshot.StreamUID ||
		selection.BranchID != snapshot.BranchID || selection.BranchGeneration != snapshot.BranchGeneration {
		return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileSource
	}
	if _, err := s.validateNativeScientificSourceParent(ctx, selection.nativeScientificSourceParent, false, false); err != nil {
		return agentPublicScientificSourceBinding{}, err
	}
	source, found, err := s.transcriptStore.GetProjectedEventByID(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: selection.StreamUID, OwnerID: selection.OwnerID, BranchID: snapshot.BranchID,
		BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence,
	}, selection.SourceEventID)
	if err != nil || !found || source.Event.PublicationSeq >= event.Event.PublicationSeq ||
		scientificSourceDigest(agentPublicScientificSourceEventPayload(source)) != selection.SourcePayloadSHA256 {
		return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileSource
	}
	binding, matched, err := s.agentPublicScientificCompletedSourceBinding(ctx, selection.OwnerID, snapshot, source, request)
	if err != nil || !matched || binding.ToolCallID != selection.SourceToolCallID {
		return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileSource
	}
	return binding, nil
}
