package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

const listPDFAnnotationsToolName = "list_pdf_annotations"

type pdfAnnotationAuthority struct {
	binding transcriptstore.PDFReadingBinding
	source  workspace.ArtifactAnnotationSource
	scope   string
}

type pdfAnnotationCursor struct {
	Version int    `json:"v"`
	Scope   string `json:"scope"`
	workspace.AnnotationPageCursor
}

func agentPDFAnnotationListToolSchema() agentruntime.ToolSchema {
	return agentruntime.ToolSchema{Name: listPDFAnnotationsToolName,
		Description: "Read one bounded page of saved annotations from an artifact-version PDF explicitly admitted to the current Reading input. Omit documentId only when exactly one document was admitted. This does not read PDF content, edit annotations, or grant filesystem access. Library attachment sources are not supported by this tool.",
		Parameters: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"documentId": map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "description": "An exact binding ID from this task's admitted PDF Reading snapshot."},
			"cursor":     map[string]any{"type": "string", "minLength": 1, "maxLength": 4096, "description": "The opaque nextCursor returned for this same task input and document."},
		}}}
}

func parsePDFAnnotationListInput(input map[string]any) (string, string, error) {
	var document, cursor string
	for key, value := range input {
		text, ok := value.(string)
		if !ok {
			return "", "", kernelruntime.NewHostCallError("invalid_arguments", "PDF annotation input fields must be strings")
		}
		switch key {
		case "documentId":
			document = strings.TrimSpace(text)
			if document == "" || len(document) > 512 {
				return "", "", kernelruntime.NewHostCallError("invalid_arguments", "documentId must identify an admitted Reading binding")
			}
		case "cursor":
			cursor = text
			if cursor == "" || len(cursor) > 4096 {
				return "", "", kernelruntime.NewHostCallError("invalid_arguments", "annotation cursor is invalid")
			}
		default:
			return "", "", kernelruntime.NewHostCallError("invalid_arguments", "PDF annotation input contains unsupported fields")
		}
	}
	return document, cursor, nil
}

func (g serverAgentRuntimeToolGateway) pdfAnnotationAuthority(ctx context.Context, document string) (pdfAnnotationAuthority, error) {
	var authority pdfAnnotationAuthority
	if ctx == nil || g.server == nil || g.server.workspaceStore == nil || g.server.transcriptStore == nil || g.taskRun == nil || g.taskRun.Transcript == nil {
		return authority, kernelruntime.NewHostCallError("reading_context_unavailable", "A live task with an admitted PDF Reading input is required")
	}
	stream, claim := g.taskRun.Transcript.Stream, g.taskRun.Transcript.Claim
	var bound workspace.KernelFrameAccess
	if g.kernel != nil {
		bound = g.kernel.access
	} else {
		// This canonical frame read grants no process, environment or path
		// capability. Never install it as the execution gateway's kernel.
		access, found, err := g.server.workspaceStore.GetKernelFrameAccessContext(ctx, stream.FrameID)
		if err != nil {
			return authority, err
		}
		if !found {
			return authority, kernelruntime.NewHostCallError("permission_denied", "PDF Reading frame is unavailable")
		}
		bound = access
	}
	access, err := g.server.validateKernelHostIdentity(ctx, bound)
	if err != nil {
		return authority, err
	}
	if stream.UID != claim.StreamUID || stream.OwnerID != claim.OwnerID || stream.OwnerID != access.UserID || stream.ProjectID != access.Frame.ProjectID ||
		stream.FrameID != access.Frame.ID || stream.RootFrameID != access.Frame.RootFrameID {
		return authority, kernelruntime.NewHostCallError("permission_denied", "PDF Reading task identity does not match its live host")
	}
	input, err := g.server.transcriptStore.ClaimedRunnerInputContext(ctx, claim)
	if err != nil {
		return authority, err
	}
	if input == nil || input.InputData[transcriptstore.PDFReadingSnapshotKey] == nil {
		return authority, kernelruntime.NewHostCallError("reading_context_unavailable", "This admitted task input has no PDF Reading binding")
	}
	snapshot, err := transcriptstore.DecodePDFReadingSnapshot(input.InputData[transcriptstore.PDFReadingSnapshotKey])
	if err != nil {
		return authority, err
	}
	expected := transcriptstore.PDFReadingScope{FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID,
		RootFrameID: access.Frame.RootFrameID, RootFrameIncarnationID: access.RootFrameIncarnationID, ProjectID: access.Frame.ProjectID}
	if snapshot == nil || snapshot.Scope != expected {
		return authority, kernelruntime.NewHostCallError("permission_denied", "Admitted PDF Reading scope changed")
	}
	if document == "" {
		if len(snapshot.Bindings) != 1 {
			return authority, kernelruntime.NewHostCallError("invalid_arguments", "Choose documentId when more than one PDF Reading binding was admitted")
		}
		document = snapshot.Bindings[0].BindingID
	}
	for _, binding := range snapshot.Bindings {
		if binding.BindingID == document {
			authority.binding = binding
			break
		}
	}
	if authority.binding.BindingID == "" {
		return authority, kernelruntime.NewHostCallError("permission_denied", "documentId is not part of this admitted Reading input")
	}
	if authority.binding.SourceKind != "artifact-version" {
		return authority, kernelruntime.NewHostCallError("unsupported_source", "Library attachment PDF annotation listing is not supported")
	}
	sourceBinding := authority.binding
	authority.source = workspace.ArtifactAnnotationSource{OwnerID: access.UserID, ProjectID: access.Frame.ProjectID,
		ArtifactID: sourceBinding.ArtifactID, VersionID: sourceBinding.VersionID, Checksum: sourceBinding.Checksum, SizeBytes: sourceBinding.SizeBytes}
	// This is a cursor consistency fingerprint, never a capability or claim token.
	raw, err := json.Marshal([]any{stream.UID, stream.OwnerID, stream.SessionID, stream.Epoch, expected, claim.RunnerID,
		claim.Attempt, claim.ClaimedInputRevision, input.EventID, input.PublicationSeq, input.Association, snapshot, sourceBinding.BindingID})
	if err != nil {
		return authority, err
	}
	sum := sha256.Sum256(raw)
	authority.scope = hex.EncodeToString(sum[:])
	return authority, nil
}

func decodePDFAnnotationCursor(raw, scope string) (*workspace.AnnotationPageCursor, error) {
	if raw == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, workspace.ErrAnnotationReadCursor
	}
	var cursor pdfAnnotationCursor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return nil, workspace.ErrAnnotationReadCursor
	}
	if decoder.Decode(new(any)) != io.EOF || cursor.Version != 1 || cursor.Scope != scope || cursor.ID == "" || cursor.LabelIndex < 0 || cursor.CreatedAt.IsZero() {
		return nil, workspace.ErrAnnotationReadCursor
	}
	return &cursor.AnnotationPageCursor, nil
}

func pdfAnnotationPreview(value any, limit int) (string, bool) {
	text, _ := value.(string)
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]), true
	}
	return text, false
}

func pdfAnnotationReadError(err error) error {
	if errors.Is(err, workspace.ErrAnnotationReadSource) {
		return kernelruntime.NewHostCallError("reading_source_unavailable", "The admitted PDF annotation source is no longer available")
	}
	if errors.Is(err, workspace.ErrAnnotationReadCursor) {
		return kernelruntime.NewHostCallError("invalid_arguments", "Annotation cursor changed; restart this document's annotation listing")
	}
	return err
}

func (g serverAgentRuntimeToolGateway) executePDFAnnotationList(ctx context.Context, input map[string]any) (any, error) {
	return g.readPDFAnnotationList(ctx, input, func(ctx context.Context, source workspace.ArtifactAnnotationSource, cursor *workspace.AnnotationPageCursor) (workspace.AnnotationReadPage, error) {
		return g.server.workspaceStore.ListArtifactAnnotationPageOwned(ctx, source, cursor)
	})
}

// The read dependency is the existing store operation. Keeping the read and
// post-read authority fence together also makes late cancellation testable
// without a second state owner or a runtime hook.
func (g serverAgentRuntimeToolGateway) readPDFAnnotationList(ctx context.Context, input map[string]any,
	read func(context.Context, workspace.ArtifactAnnotationSource, *workspace.AnnotationPageCursor) (workspace.AnnotationReadPage, error),
) (any, error) {
	document, rawCursor, err := parsePDFAnnotationListInput(input)
	if err != nil {
		return nil, err
	}
	authority, err := g.pdfAnnotationAuthority(ctx, document)
	if err != nil {
		return nil, err
	}
	cursor, err := decodePDFAnnotationCursor(rawCursor, authority.scope)
	if err != nil {
		return nil, kernelruntime.NewHostCallError("invalid_arguments", "Use a fresh annotation cursor from this same task input and PDF binding")
	}
	page, err := read(ctx, authority.source, cursor)
	if err != nil {
		return nil, pdfAnnotationReadError(err)
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, annotation := range page.Items {
		note, noteTruncated := pdfAnnotationPreview(annotation.Body["text"], 1000)
		quote, quoteTruncated := pdfAnnotationPreview(annotation.Body["selection_text"], 500)
		item := map[string]any{"id": annotation.ID, "label": annotation.Public()["label"], "type": annotation.Body["type"],
			"note": note, "noteTruncated": noteTruncated, "quote": quote, "quoteTruncated": quoteTruncated}
		if value := annotation.Body["page_number"]; value != nil {
			item["pageNumber"] = value
		}
		items = append(items, item)
	}
	var next any
	if page.Next != nil {
		raw, err := json.Marshal(pdfAnnotationCursor{Version: 1, Scope: authority.scope, AnnotationPageCursor: *page.Next})
		if err != nil {
			return nil, err
		}
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	current, err := g.pdfAnnotationAuthority(ctx, authority.binding.BindingID)
	if err != nil {
		return nil, err
	}
	if current.scope != authority.scope {
		return nil, kernelruntime.NewHostCallError("permission_denied", "PDF Reading task scope changed during annotation listing")
	}
	if err := g.server.workspaceStore.ValidateArtifactAnnotationSourceOwned(ctx, current.source); err != nil {
		return nil, pdfAnnotationReadError(err)
	}
	return map[string]any{"documentId": authority.binding.BindingID, "readOnly": true, "total": page.Total, "nextCursor": next, "annotations": items}, nil
}
