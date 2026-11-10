package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	transcriptstore "x-science/internal/persistence/transcript"
)

// This internal receipt is deliberately not a root tool lifecycle or MCP
// attestation. Its parent is the original immutable native download batch.
type nativeScientificAcquisition struct {
	nativeScientificSourceParent
	Schema        string          `json:"schema"`
	Kind          string          `json:"kind,omitempty"`
	ToolPhase     string          `json:"toolPhase"`
	ToolName      string          `json:"toolName"`
	ToolCallID    string          `json:"toolCallId"`
	ToolInput     json.RawMessage `json:"toolInput"`
	ToolResult    json.RawMessage `json:"toolResult,omitempty"`
	RequestSHA256 string          `json:"requestSha256"`
	ResultSHA256  string          `json:"resultSha256,omitempty"`
}

func scientificSourceDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (phase nativeScientificAcquisition) identity() string {
	raw, _ := json.Marshal([]any{agentPublicScientificAcquisitionSchema, phase.StreamUID, phase.OwnerID,
		phase.BranchID, phase.BranchGeneration, phase.ParentBatchID, phase.ParentOrdinal,
		phase.ParentToolCallID, phase.ParentSourceEventID, phase.ParentArgumentsSHA256,
		phase.ParentExecutedSHA256, phase.SourceURL, phase.RequestSHA256})
	return "download-source-" + scientificSourceDigest(raw)[:32]
}

func (s *Server) newNativeScientificAcquisition(ctx context.Context, task *sessionRunnerChatRun, run transcriptArtifactRun, parentID string, input map[string]any, request agentPublicScientificFileRequest) (nativeScientificAcquisition, error) {
	parent, err := s.newNativeScientificSourceParent(ctx, task, run, parentID, input, true)
	phase := nativeScientificAcquisition{nativeScientificSourceParent: parent, Schema: agentPublicScientificAcquisitionSchema, ToolName: "web_fetch"}
	if err != nil {
		return phase, err
	}
	phase.ToolInput, _ = json.Marshal(map[string]any{"url": request.DownloadURL, "limit": nativeScientificSourceLimit})
	phase.RequestSHA256 = scientificSourceDigest(phase.ToolInput)
	phase.ToolCallID = phase.identity()
	_, err = s.validateNativeScientificAcquisitionParent(ctx, phase, true)
	return phase, err
}

// The frozen branch reader checks the actual started parent, original batch
// arguments and current branch, rather than trusting self-described JSON IDs.
// Historical completed evidence may outlive the producing claim; appending a
// new phase separately requires the original live runner checkpoint owner.
func (s *Server) validateNativeScientificAcquisitionParent(ctx context.Context, phase nativeScientificAcquisition, requireRunning bool) (transcriptstore.ProjectionSnapshot, error) {
	if phase.Schema != agentPublicScientificAcquisitionSchema || phase.Kind != "" || phase.ToolName != "web_fetch" || phase.ToolCallID != phase.identity() ||
		scientificSourceDigest(phase.ToolInput) != phase.RequestSHA256 {
		return transcriptstore.ProjectionSnapshot{}, errAgentPublicScientificFileAuthority
	}
	var executed map[string]any
	if json.Unmarshal(phase.ParentExecutedInput, &executed) != nil {
		return transcriptstore.ProjectionSnapshot{}, errAgentPublicScientificFileAuthority
	}
	request, err := parseAgentPublicScientificFileRequest(executed)
	expected, _ := json.Marshal(map[string]any{"url": request.DownloadURL, "limit": nativeScientificSourceLimit})
	if err != nil || request.SourceURL != phase.SourceURL || string(expected) != string(phase.ToolInput) {
		return transcriptstore.ProjectionSnapshot{}, errAgentPublicScientificFileAuthority
	}
	return s.validateNativeScientificSourceParent(ctx, phase.nativeScientificSourceParent, requireRunning, true)
}

func (s *Server) nativeScientificAcquisitionSource(ctx context.Context, snapshot transcriptstore.ProjectionSnapshot, event transcriptstore.ProjectedEvent, expectedURL string) (agentPublicScientificSourceBinding, any, bool, error) {
	var phase nativeScientificAcquisition
	fail := func() (agentPublicScientificSourceBinding, any, bool, error) {
		return agentPublicScientificSourceBinding{}, nil, false, errAgentPublicScientificFileSource
	}
	if json.Unmarshal(event.Event.PayloadJSON, &phase) != nil {
		return fail()
	}
	if phase.SourceURL != expectedURL {
		// A damaged label cannot hide a receipt for this very parent input and
		// turn rejected provenance into a fresh acquisition. Unrelated sources
		// still remain outside this lookup.
		var executed map[string]any
		if json.Unmarshal(phase.ParentExecutedInput, &executed) == nil {
			if request, err := parseAgentPublicScientificFileRequest(executed); err == nil && request.SourceURL == expectedURL {
				return fail()
			}
		}
		return agentPublicScientificSourceBinding{}, nil, false, nil
	}
	if phase.StreamUID != snapshot.StreamUID || event.Event.StreamUID != snapshot.StreamUID ||
		phase.BranchID != snapshot.BranchID || phase.BranchGeneration != snapshot.BranchGeneration {
		return fail()
	}
	if _, err := s.validateNativeScientificAcquisitionParent(ctx, phase, false); err != nil {
		return fail()
	}
	if phase.ToolPhase == "source_acquisition_start" && len(phase.ToolResult) == 0 && phase.ResultSHA256 == "" {
		return agentPublicScientificSourceBinding{}, nil, false, nil
	}
	if phase.ToolPhase != "source_acquisition_completed" || phase.ResultSHA256 != scientificSourceDigest(phase.ToolResult) {
		return fail()
	}
	result, err := s.agentPublicScientificSourceResult(ctx, phase.OwnerID, phase.ToolResult)
	if err != nil || !agentPublicScientificResultAuthorizesDownload(result, expectedURL) {
		return fail()
	}
	binding := agentPublicScientificSourceEventBinding(phase.ToolCallID, event)
	if canonical, hosts, verified := agentPublicScientificWebFetchRedirectBinding(result); verified && agentPublicScientificURLsEquivalent(canonical, expectedURL) {
		binding.RedirectHosts = hosts
	}
	return binding, result, true, nil
}
