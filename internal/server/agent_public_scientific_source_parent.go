package server

import (
	"bytes"
	"context"
	"encoding/json"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// The existing started download and runner claim own both private variants.
// Selecting a source never manufactures a model batch or a web_fetch call.
type nativeScientificSourceParent struct {
	StreamUID             string          `json:"streamUID"`
	OwnerID               string          `json:"ownerID"`
	BranchID              string          `json:"branchID"`
	BranchGeneration      int64           `json:"branchGeneration"`
	ParentBatchID         string          `json:"parentBatchID"`
	ParentOrdinal         int64           `json:"parentOrdinal"`
	ParentToolCallID      string          `json:"parentToolCallId"`
	ParentSourceEventID   int64           `json:"parentSourceEventId"`
	ParentArgumentsSHA256 string          `json:"parentArgumentsSha256"`
	ParentExecutedInput   json.RawMessage `json:"parentExecutedInput"`
	ParentExecutedSHA256  string          `json:"parentExecutedSha256"`
	SourceURL             string          `json:"sourceURL"`
}

func (s *Server) appendNativeScientificSourceFact(ctx context.Context, run transcriptArtifactRun, parent nativeScientificSourceParent, identity string, value any, requireBatch bool) error {
	if _, err := s.transcriptStore.ValidateRunnerToolArtifactSource(ctx, run.Authority.Claim, parent.ParentSourceEventID,
		parent.ParentToolCallID, "download_public_scientific_file"); err != nil {
		return err
	}
	if _, err := s.validateNativeScientificSourceParent(ctx, parent, true, requireBatch); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return err
	}
	_, err = s.checkpointTranscriptRunnerEvent(ctx, run.Authority, transcriptstore.RunnerPhaseExecuting, identity, payload, true)
	return err
}

func (s *Server) newNativeScientificSourceParent(ctx context.Context, task *sessionRunnerChatRun, run transcriptArtifactRun, parentID string, input map[string]any, requireBatch bool) (nativeScientificSourceParent, error) {
	if s == nil || s.transcriptStore == nil || s.workspaceStore == nil || run.Authority == nil {
		return nativeScientificSourceParent{}, errAgentPublicScientificFileAuthority
	}
	parent := nativeScientificSourceParent{StreamUID: run.Authority.Stream.UID, OwnerID: run.Authority.Stream.OwnerID,
		ParentToolCallID: parentID, ParentSourceEventID: run.SourceEventID}
	snapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, parent.StreamUID, parent.OwnerID)
	if err != nil {
		return parent, err
	}
	parent.BranchID, parent.BranchGeneration = snapshot.BranchID, snapshot.BranchGeneration
	started, err := s.nativeScientificStartedParent(ctx, snapshot, parent)
	if err != nil {
		return parent, err
	}
	var original map[string]any
	if json.Unmarshal(started.ToolInput, &original) != nil {
		return parent, errAgentPublicScientificFileAuthority
	}
	parent.ParentArgumentsSHA256 = scientificSourceDigest(mustMarshalRawMessage(original))
	parent.ParentExecutedInput, err = json.Marshal(input)
	if err != nil {
		return parent, err
	}
	parent.ParentExecutedSHA256 = scientificSourceDigest(parent.ParentExecutedInput)
	request, err := parseAgentPublicScientificFileRequest(input)
	if err != nil {
		return parent, err
	}
	parent.SourceURL = request.SourceURL
	if task != nil {
		parent.ParentBatchID = task.ToolBatchIDs[parentID]
		var found bool
		parent.ParentOrdinal, found = task.ToolBatchOrdinals[parentID]
		if (parent.ParentBatchID != "") != found {
			return parent, errAgentPublicScientificFileAuthority
		}
	}
	if requireBatch {
		batch, found, batchErr := s.workspaceStore.GetToolCallBatch(ctx, parent.OwnerID, parent.ParentBatchID)
		if batchErr != nil || !found || batch.AdmittedInputRevision != run.Authority.Claim.ClaimedInputRevision {
			return parent, errAgentPublicScientificFileAuthority
		}
	}
	_, err = s.validateNativeScientificSourceParent(ctx, parent, true, requireBatch)
	return parent, err
}

func (s *Server) nativeScientificStartedParent(ctx context.Context, snapshot transcriptstore.ProjectionSnapshot, parent nativeScientificSourceParent) (sessionRunnerDurableToolCheckpoint, error) {
	event, found, err := s.transcriptStore.GetProjectedEventByID(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: parent.StreamUID, OwnerID: parent.OwnerID, BranchID: snapshot.BranchID,
		BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence,
	}, parent.ParentSourceEventID)
	var started sessionRunnerDurableToolCheckpoint
	if err != nil || !found || event.Event.Type != "runner_checkpoint" || event.Event.StreamUID != parent.StreamUID ||
		json.Unmarshal(event.Event.PayloadJSON, &started) != nil || started.ToolName != "download_public_scientific_file" ||
		started.ToolCallID != parent.ParentToolCallID || started.ToolPhase != "start" {
		return started, errAgentPublicScientificFileAuthority
	}
	return started, nil
}

func (s *Server) validateNativeScientificSourceParent(ctx context.Context, parent nativeScientificSourceParent, requireRunning, requireBatch bool) (transcriptstore.ProjectionSnapshot, error) {
	fail := func() (transcriptstore.ProjectionSnapshot, error) {
		return transcriptstore.ProjectionSnapshot{}, errAgentPublicScientificFileAuthority
	}
	if s == nil || s.transcriptStore == nil || s.workspaceStore == nil || parent.ParentSourceEventID <= 0 ||
		!validSHA256Hex(parent.ParentArgumentsSHA256) ||
		scientificSourceDigest(parent.ParentExecutedInput) != parent.ParentExecutedSHA256 {
		return fail()
	}
	var executed map[string]any
	if json.Unmarshal(parent.ParentExecutedInput, &executed) != nil {
		return fail()
	}
	request, err := parseAgentPublicScientificFileRequest(executed)
	if err != nil || request.SourceURL != parent.SourceURL {
		return fail()
	}
	snapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, parent.StreamUID, parent.OwnerID)
	if err != nil || snapshot.BranchID != parent.BranchID || snapshot.BranchGeneration != parent.BranchGeneration {
		return fail()
	}
	if task, ok := transcriptRunnerChatRunFromContext(ctx); ok && task.ToolBatchIDs[parent.ParentToolCallID] != "" {
		expectedOrdinal, found := task.ToolBatchOrdinals[parent.ParentToolCallID]
		if !found || parent.ParentBatchID != task.ToolBatchIDs[parent.ParentToolCallID] || parent.ParentOrdinal != expectedOrdinal {
			return fail()
		}
	}
	if requireBatch || parent.ParentBatchID != "" {
		batch, found, err := s.workspaceStore.GetToolCallBatch(ctx, parent.OwnerID, parent.ParentBatchID)
		if err != nil || !found || batch.OwnerUserID != parent.OwnerID || batch.StreamUID != parent.StreamUID ||
			batch.BranchID != parent.BranchID || batch.BranchGeneration != parent.BranchGeneration {
			return fail()
		}
		items, err := s.workspaceStore.ListToolCallBatchItems(ctx, parent.OwnerID, parent.ParentBatchID)
		if err != nil {
			return fail()
		}
		found = false
		for _, item := range items {
			if item.Ordinal == parent.ParentOrdinal && item.ToolCallID == parent.ParentToolCallID && item.ToolName == "download_public_scientific_file" &&
				item.StartedEventID == parent.ParentSourceEventID && item.ArgumentsSHA256 == parent.ParentArgumentsSHA256 &&
				scientificSourceDigest(item.ArgumentsJSON) == item.ArgumentsSHA256 {
				found = (!requireRunning || batch.State == workspace.ToolCallBatchStateRunning && item.State == workspace.ToolCallBatchItemStateRunning)
				if requireRunning {
					if run, ok := transcriptRunnerChatRunFromContext(ctx); ok {
						found = found && batch.AdmittedInputRevision == run.Transcript.Claim.ClaimedInputRevision
					}
				}
			}
		}
		if !found {
			return fail()
		}
	}
	started, err := s.nativeScientificStartedParent(ctx, snapshot, parent)
	var original map[string]any
	if err != nil || json.Unmarshal(started.ToolInput, &original) != nil ||
		scientificSourceDigest(mustMarshalRawMessage(original)) != parent.ParentArgumentsSHA256 {
		return fail()
	}
	return snapshot, nil
}
