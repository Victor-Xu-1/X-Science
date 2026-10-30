package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func writeLiteratureError(w http.ResponseWriter, err error) {
	status, code := 500, "LITERATURE_INTERNAL"
	switch {
	case errors.Is(err, workspace.ErrLiteratureInvalid):
		status, code = 400, "LITERATURE_INVALID"
	case errors.Is(err, workspace.ErrLiteratureUnavailable):
		status, code = 404, "LITERATURE_UNAVAILABLE"
	case errors.Is(err, workspace.ErrLiteratureCollectionNameConflict):
		status, code = 409, "LITERATURE_COLLECTION_NAME_CONFLICT"
	case errors.Is(err, workspace.ErrLiteratureTrashConflict):
		status, code = 409, "LITERATURE_TRASH_CONFLICT"
	case errors.Is(err, workspace.ErrLiteratureConflict), errors.Is(err, workspace.ErrMutationIdempotencyConflict):
		status, code = 409, "LITERATURE_CONFLICT"
	}
	writeWorkspaceJSON(w, status, map[string]any{"code": code})
}
func validLiteratureOperation(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func (s *Server) handleLiterature(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, ok := attachmentUserID(w, r)
	if !ok {
		return
	}
	if s.workspaceStore == nil {
		writeLiteratureError(w, errors.New("unavailable"))
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/literature"), "/"), "/")
	if len(parts) == 1 && parts[0] == "related" && r.Method == http.MethodPost {
		s.handleLiteratureRelated(w, r, owner)
		return
	}
	if s.handleLiteratureCandidates(w, r, owner, parts) {
		return
	}
	if s.handleLiteratureCollectionRoute(w, r, owner, parts) {
		return
	}
	if len(parts) == 2 && parts[0] == "items" && parts[1] == "reading-status" {
		s.handleLiteratureReadingStatus(w, r, owner)
		return
	}
	if len(parts) == 3 && parts[0] == "items" && parts[2] == "publication-check" {
		s.handleLiteraturePublication(w, r, owner, parts[1])
		return
	}
	if len(parts) == 1 && parts[0] == "export-bibtex" && r.Method == http.MethodPost {
		s.handleLiteratureBibtex(w, r, owner)
		return
	}
	if len(parts) == 1 && parts[0] == "items" && r.Method == http.MethodGet {
		s.handleLiteratureItemsRead(w, r, owner)
		return
	}
	if len(parts) == 1 && parts[0] == "items" && r.Method == http.MethodPost {
		var input workspace.LiteratureManualCreate
		if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		receipt, err := s.workspaceStore.CreateManualLiterature(r.Context(), owner, input)
		if err != nil {
			writeLiteratureError(w, err)
			return
		}
		writeWorkspaceJSON(w, 200, receipt)
		return
	}
	if len(parts) == 1 && parts[0] == "import-artifact-version" && r.Method == http.MethodPost {
		s.handleLiteratureImport(w, r, owner)
		return
	}
	if len(parts) == 3 && parts[0] == "attachment-versions" && parts[2] == "content" && r.Method == http.MethodGet {
		frame := r.URL.Query().Get("frame_id")
		if len(r.URL.Query()) > 1 || len(r.URL.Query()) == 1 && (len(r.URL.Query()["frame_id"]) != 1 || frame == "" || len(frame) > 512 || strings.TrimSpace(frame) != frame) {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		var accepted *transcriptstore.PDFReadingBinding
		// An ordinary owner-authorized live preview does not require sending a
		// message first. Only retained/tombstoned access needs accepted proof.
		item, version, reader, err := s.workspaceStore.OpenLiteratureVersionContent(r.Context(), owner, parts[1], false)
		if err != nil && frame != "" && errors.Is(err, workspace.ErrLiteratureUnavailable) {
			binding, proofErr := s.workspaceStore.AcceptedLiteratureReadingBinding(r.Context(), owner, frame, parts[1])
			if proofErr != nil {
				writeLiteratureError(w, workspace.ErrLiteratureUnavailable)
				return
			}
			accepted = &binding
			item, version, reader, err = s.workspaceStore.OpenLiteratureVersionContent(r.Context(), owner, parts[1], true)
		}
		if err != nil {
			writeLiteratureError(w, err)
			return
		}
		defer reader.Close()
		if accepted != nil && (accepted.ItemID != item || accepted.AttachmentID != version.AttachmentID || accepted.Checksum != version.Checksum || accepted.SizeBytes != version.SizeBytes) {
			writeLiteratureError(w, workspace.ErrLiteratureUnavailable)
			return
		}
		w.Header().Set("Content-Type", version.MIMEType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, version.Filename, time.Time{}, reader)
		return
	}
	if len(parts) == 2 && parts[0] == "items" && r.Method == http.MethodGet {
		s.handleLiteratureItemRead(w, r, owner, parts[1])
		return
	}
	if len(parts) == 2 && parts[0] == "items" && r.Method == http.MethodPatch {
		var input workspace.LiteraturePatch
		if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		receipt, err := s.workspaceStore.PatchLiteratureItem(r.Context(), owner, parts[1], input)
		if err != nil {
			writeLiteratureError(w, err)
			return
		}
		writeWorkspaceJSON(w, 200, receipt)
		return
	}
	if len(parts) == 4 && parts[0] == "items" && parts[2] == "projects" && r.Method == http.MethodPut {
		var input struct {
			OperationID string `json:"operation_id"`
			Included    *bool  `json:"included"`
		}
		if decodePDFReadingBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) || input.Included == nil {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		receipt, err := s.workspaceStore.SetLiteratureProject(r.Context(), owner, parts[1], parts[3], input.OperationID, *input.Included)
		if err != nil {
			writeLiteratureError(w, err)
			return
		}
		writeWorkspaceJSON(w, 200, receipt)
		return
	}
	writeWorkspaceJSON(w, 404, map[string]any{"code": "LITERATURE_UNAVAILABLE"})
}

func (s *Server) handleLiteratureImport(w http.ResponseWriter, r *http.Request, owner string) {
	var input workspace.LiteratureImport
	if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	// A committed original retry is resolvable even after its source project is
	// deleted. PageCount=0 causes a new request to stop before staging/writing.
	receipt, err := s.workspaceStore.ImportLiteratureArtifact(r.Context(), owner, input)
	if err == nil {
		writeWorkspaceJSON(w, 200, receipt)
		return
	}
	if !errors.Is(err, workspace.ErrLiteratureInvalid) {
		writeLiteratureError(w, err)
		return
	}
	artifact, _, reader, found, err := s.workspaceStore.OpenArtifactVersionContentForOwner(input.VersionID, owner)
	if err != nil || !found || artifact.ID != input.ArtifactID {
		if reader != nil {
			_ = reader.Close()
		}
		writeLiteratureError(w, workspace.ErrLiteratureUnavailable)
		return
	}
	_ = reader.Close()
	binding, err := s.inspectPDFSourceDocument(r.Context(), owner, artifact.ProjectID, transcriptstore.PDFReadingSource{ArtifactID: input.ArtifactID, VersionID: input.VersionID})
	if err != nil {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	input.PageCount = binding.PageCount
	receipt, err = s.workspaceStore.ImportLiteratureArtifact(r.Context(), owner, input)
	if err != nil {
		writeLiteratureError(w, err)
		return
	}
	writeWorkspaceJSON(w, 200, receipt)
}
