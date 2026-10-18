package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

type pdfReadingMutationBody struct {
	Schema      string `json:"schema"`
	OperationID string `json:"operation_id"`
	transcriptstore.PDFReadingExpected
	Sources   []transcriptstore.PDFReadingSource `json:"sources,omitempty"`
	BindingID string                             `json:"binding_id,omitempty"`
}

type pdfReadingCandidatesBody struct {
	Schema  string                             `json:"schema"`
	Sources []transcriptstore.PDFReadingSource `json:"sources"`
}

func validPDFReadingSources(sources []transcriptstore.PDFReadingSource, max int) bool {
	if len(sources) > max {
		return false
	}
	for _, source := range sources {
		for _, value := range []string{source.ArtifactID, source.VersionID} {
			if value == "" || len(value) > 512 || value != strings.TrimSpace(value) {
				return false
			}
		}
	}
	return true
}

func decodePDFReadingBody(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	var trailing any
	if len(r.URL.Query()) != 0 || decoder.Decode(value) != nil || decoder.Decode(&trailing) != io.EOF {
		return transcriptstore.ErrPDFReadingInvalid
	}
	return nil
}

func (s *Server) handlePDFReadingContext(w http.ResponseWriter, r *http.Request, frame workspace.CompatibilityFrame, tail []string) {
	w.Header().Set("Cache-Control", "no-store")
	owner := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	current, err := s.workspaceStore.GetPDFReadingContext(r.Context(), owner, frame.ID)
	if err != nil {
		writePDFReadingError(w, err)
		return
	}
	if len(tail) == 0 && r.Method == http.MethodGet {
		if len(r.URL.Query()) != 0 {
			writePDFReadingError(w, transcriptstore.ErrPDFReadingInvalid)
			return
		}
		writeWorkspaceJSON(w, 200, current)
		return
	}
	if len(tail) != 1 || r.Method != http.MethodPost {
		writeWorkspaceJSON(w, 405, map[string]any{"code": "METHOD_NOT_ALLOWED"})
		return
	}
	if tail[0] == "candidates" {
		s.handlePDFReadingCandidates(w, r, current, owner)
		return
	}
	if tail[0] != "link" && tail[0] != "unlink" {
		writeWorkspaceJSON(w, 404, map[string]any{"code": "NOT_FOUND"})
		return
	}
	var body pdfReadingMutationBody
	if decodePDFReadingBody(w, r, &body) != nil || body.Schema != "x-science.pdf-reading-"+tail[0]+".v1" || !body.PDFReadingExpected.Valid() {
		writePDFReadingError(w, transcriptstore.ErrPDFReadingInvalid)
		return
	}
	operation, err := uuid.Parse(body.OperationID)
	link := tail[0] == "link"
	if err != nil || operation.String() != body.OperationID || link && (len(body.Sources) == 0 || !validPDFReadingSources(body.Sources, 3) || body.BindingID != "") ||
		!link && (len(body.Sources) != 0 || body.BindingID == "" || len(body.BindingID) > 512) {
		writePDFReadingError(w, transcriptstore.ErrPDFReadingInvalid)
		return
	}
	input := workspace.PDFReadingMutation{OwnerID: owner, FrameID: frame.ID, OperationID: body.OperationID, Expected: body.PDFReadingExpected, Sources: body.Sources, BindingID: body.BindingID}
	replay, err := workspace.PDFReadingMutationReplay(current, input, link)
	if err != nil {
		writePDFReadingError(w, err)
		return
	}
	if replay {
		writeWorkspaceJSON(w, 200, current)
		return
	}
	if link {
		existing := map[string]transcriptstore.PDFReadingBinding{}
		for _, binding := range current.Bindings {
			existing[binding.VersionID] = binding
		}
		seen := map[string]bool{}
		for _, source := range body.Sources {
			if seen[source.VersionID] {
				writePDFReadingError(w, transcriptstore.ErrPDFReadingInvalid)
				return
			}
			seen[source.VersionID] = true
			binding, found := existing[source.VersionID]
			if found && binding.ArtifactID != source.ArtifactID {
				writePDFReadingError(w, transcriptstore.ErrPDFReadingUnavailable)
				return
			}
			if !found {
				binding, err = s.inspectPDFReadingSource(r.Context(), owner, current.Scope.ProjectID, source)
				if err != nil {
					writePDFReadingError(w, err)
					return
				}
			}
			input.Bindings = append(input.Bindings, binding)
		}
		current, err = s.workspaceStore.LinkPDFReadingContext(r.Context(), input)
	} else {
		current, err = s.workspaceStore.UnlinkPDFReadingContext(r.Context(), input)
	}
	if err != nil {
		writePDFReadingError(w, err)
		return
	}
	writeWorkspaceJSON(w, 200, current)
}
