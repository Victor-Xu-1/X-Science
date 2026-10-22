package server

import (
	"errors"
	"net/http"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Server) handlePDFReadingCandidates(w http.ResponseWriter, r *http.Request, current transcriptstore.PDFReadingContext, owner string) {
	var body pdfReadingCandidatesBody
	if decodePDFReadingBody(w, r, &body) != nil || body.Schema != "x-science.pdf-reading-candidates.v1" || !validPDFReadingSources(body.Sources, 100) {
		writePDFReadingError(w, transcriptstore.ErrPDFReadingInvalid)
		return
	}
	candidates := []map[string]any{}
	seen := map[string]bool{}
	for _, source := range body.Sources {
		key := source.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		binding, err := s.inspectPDFReadingSource(r.Context(), owner, current.Scope.ProjectID, source)
		entry := map[string]any{"source": source, "eligible": err == nil}
		if err != nil {
			var unavailable *pdfReadingCandidateError
			if !errors.As(err, &unavailable) {
				writePDFReadingError(w, err)
				return
			}
			entry["reason"] = unavailable.reason
		} else {
			entry["page_count"], entry["size_bytes"], entry["checksum"] = binding.PageCount, binding.SizeBytes, binding.Checksum
		}
		candidates = append(candidates, entry)
	}
	writeWorkspaceJSON(w, 200, map[string]any{"schema": "x-science.pdf-reading-candidates.v1", "scope": current.Scope, "revision": current.Revision, "candidates": candidates})
}

func writePDFReadingError(w http.ResponseWriter, err error) {
	status, code := 503, "PDF_READING_STORAGE_UNAVAILABLE"
	var candidate *pdfReadingCandidateError
	switch {
	case errors.Is(err, transcriptstore.ErrPDFReadingInvalid):
		status, code = 400, "PDF_READING_INVALID"
	case errors.Is(err, transcriptstore.ErrPDFReadingState):
		status, code = 503, "PDF_READING_STATE_UNAVAILABLE"
	case errors.Is(err, transcriptstore.ErrOwnerMismatch):
		status, code = 404, "PDF_READING_UNAVAILABLE"
	case errors.Is(err, transcriptstore.ErrPDFReadingScope):
		status, code = 409, "PDF_READING_SCOPE_STALE"
	case errors.Is(err, transcriptstore.ErrPDFReadingStale):
		status, code = 409, "PDF_READING_REVISION_STALE"
	case errors.Is(err, transcriptstore.ErrPDFReadingLimit):
		status, code = 409, "PDF_READING_LIMIT"
	case errors.Is(err, transcriptstore.ErrPDFReadingUnavailable), errors.As(err, &candidate):
		status, code = 409, "PDF_READING_SOURCE_UNAVAILABLE"
	case errors.Is(err, transcriptstore.ErrConversationArchived):
		status, code = 409, "CONVERSATION_ARCHIVED"
	}
	writeWorkspaceJSON(w, status, map[string]any{"code": code, "message": "PDF reading context is unavailable; the source files were not changed."})
}

func isPDFReadingError(err error) bool {
	return errors.Is(err, transcriptstore.ErrPDFReadingInvalid) || errors.Is(err, transcriptstore.ErrPDFReadingState) ||
		errors.Is(err, transcriptstore.ErrPDFReadingScope) || errors.Is(err, transcriptstore.ErrPDFReadingStale) ||
		errors.Is(err, transcriptstore.ErrPDFReadingUnavailable) || errors.Is(err, transcriptstore.ErrPDFReadingLimit)
}
