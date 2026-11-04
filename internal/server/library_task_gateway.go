package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

const librarySaveToInboxToolName = "save_to_inbox"

func librarySaveToInboxSchema() agentruntime.ToolSchema {
	return agentruntime.ToolSchema{Name: librarySaveToInboxToolName,
		Description: "Save 1-10 discovered reference metadata records to Library Inbox for the user to review. This is a first-party Library operation; it does not download PDFs or claim full-text reading. New candidates remain pending, existing accepted items are reused, dismissed candidates remain dismissed. The current task/project provenance is bound by the server. Save the original operation_id UUID and exact references for a lost-response retry. The bounded batch is atomic; pending Library review does not pause this task.",
		Parameters: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operation_id", "references"},
			"properties": map[string]any{
				"operation_id": map[string]any{"type": "string", "format": "uuid", "description": "A new UUID for this batch; reuse it with identical arguments only when retrying the same operation."},
				"references": map[string]any{"type": "array", "minItems": 1, "maxItems": 10, "items": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"provider", "external_id", "title", "metadata"},
					"properties": map[string]any{
						"provider":    map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "description": "Source provider, such as crossref, semantic-scholar, or an observed source domain."},
						"external_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "description": "Stable identifier from that source, such as DOI, provider record ID or source URL."},
						"title":       map[string]any{"type": "string", "minLength": 1, "maxLength": 4096},
						"metadata":    map[string]any{"type": "object", "description": "Observed bibliographic fields, e.g. doi, authors, year, abstract and url. These are untrusted metadata, never owner/project/task authority."},
					}}}}}}
}

func parseLibraryTaskRequest(input any) (workspace.LiteratureTaskInboxRequest, error) {
	var request workspace.LiteratureTaskInboxRequest
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 256<<10 {
		return request, workspace.ErrLiteratureInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&request); err != nil {
		return request, workspace.ErrLiteratureInvalid
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return request, workspace.ErrLiteratureInvalid
	}
	return request, nil
}

// All public tool and SDK adapters end at the same typed Store transaction.
func (s *Server) saveTaskLibraryInbox(ctx context.Context, authority workspace.LiteratureTaskAuthority, input any) (any, error) {
	if s == nil || s.workspaceStore == nil {
		return nil, kernelruntime.NewHostCallError("unavailable", "Library storage is unavailable")
	}
	request, err := parseLibraryTaskRequest(input)
	if err != nil {
		return nil, libraryTaskError(err)
	}
	receipt, err := s.workspaceStore.SaveTaskLiteratureToInbox(ctx, authority, request)
	return receipt, libraryTaskError(err)
}

func libraryTaskError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, workspace.ErrLiteratureInvalid):
		return kernelruntime.NewHostCallError("invalid_arguments", "Invalid Library metadata batch; supply 1-10 complete references and the original operation UUID")
	case errors.Is(err, workspace.ErrMutationIdempotencyConflict), errors.Is(err, workspace.ErrLiteratureConflict):
		return kernelruntime.NewHostCallError("request_conflict", "Library operation identity or reference identity conflicts; do not replace the original arguments on retry")
	case errors.Is(err, workspace.ErrLiteratureTaskAuthority), errors.Is(err, transcriptstore.ErrClaimStale), errors.Is(err, transcriptstore.ErrOwnerMismatch):
		return kernelruntime.NewHostCallError("permission_denied", "Library task execution authority is stale or unavailable")
	case errors.Is(err, workspace.ErrLiteratureUnavailable):
		return kernelruntime.NewHostCallError("unavailable", "Library reference or destination is unavailable")
	default:
		return kernelruntime.NewHostCallError("storage_error", "Library save outcome could not be confirmed; retry only with the same operation UUID and original references")
	}
}

// Capture the original frame identity at gateway creation. Metadata-only tools
// do not require a Python environment, filesystem grant or process capability.
func (s *Server) captureLibraryTaskAccess(ctx context.Context, sessionID string) *workspace.KernelFrameAccess {
	if s == nil || s.workspaceStore == nil || s.transcriptStore == nil || ctx == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	stream, authoritative, err := s.resolveTranscriptFrameStream(ctx, sessionID)
	if err != nil || !authoritative || stream.Kind != transcriptstore.StreamKindFrameRef {
		return nil
	}
	access, found, err := s.workspaceStore.GetKernelFrameAccessContext(ctx, stream.FrameID)
	if err != nil || !found || access.UserID != stream.OwnerID || access.Frame.ProjectID != stream.ProjectID || access.Frame.RootFrameID != stream.RootFrameID {
		return nil
	}
	return &access
}

func (s *Server) executeAgentLibraryInbox(ctx context.Context, identity *workspace.KernelFrameAccess, callID string, input map[string]any) (any, error) {
	run, ok := transcriptRunnerChatRunFromContext(ctx)
	if !ok || run.Transcript == nil || identity == nil || callID == "" {
		return nil, libraryTaskError(workspace.ErrLiteratureTaskAuthority)
	}
	access, err := s.validateKernelHostIdentity(ctx, *identity)
	if err != nil {
		return nil, err
	}
	return s.saveTaskLibraryInbox(ctx, workspace.LiteratureTaskAuthority{Access: access, Stream: run.Transcript.Stream, Claim: run.Transcript.Claim, ToolCallID: callID}, input)
}

func (s *Server) handleKernelLibraryInbox(ctx context.Context, bound kernelHostExecutionIdentity, current workspace.KernelFrameAccess, args []any, kwargs map[string]any) (any, error) {
	if !agentKernelToolAllowed(librarySaveToInboxToolName, bound.allowedTools) {
		return nil, kernelruntime.NewHostCallError("permission_denied", "Library save-to-Inbox is excluded by this task's tool authority")
	}
	if len(args) != 1 || len(kwargs) != 0 || bound.fresh {
		return nil, kernelruntime.NewHostCallError("invalid_arguments", "host.library.save_to_inbox requires one frozen request object in the existing task kernel")
	}
	b := bound.transcriptExecution
	if b.kernelID == "" || b.kernelGeneration == 0 || b.currentKernelGeneration == nil {
		return nil, libraryTaskError(workspace.ErrLiteratureTaskAuthority)
	}
	return s.saveTaskLibraryInbox(ctx, workspace.LiteratureTaskAuthority{Access: current, Stream: b.stream, Claim: b.claim,
		ToolCallID: b.outerToolUseID, KernelGeneration: b.kernelGeneration, CurrentKernelGeneration: b.currentKernelGeneration}, args[0])
}
