package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

// The Library route resolves its own immutable version. An origin artifact is
// provenance and cannot grant access or keep an independent Library copy alive.
func (s *Server) handleLibraryVersionAnnotations(w http.ResponseWriter, r *http.Request, owner, version string) {
	source, err := s.workspaceStore.LiteratureAnnotationSourceOwned(r.Context(), owner, version, false)
	if err != nil {
		writeAnnotationStoreError(w, err)
		return
	}
	scopeBytes, _ := json.Marshal(source)
	digest := sha256.Sum256(scopeBytes)
	scope := hex.EncodeToString(digest[:])
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		if len(query) > 1 || len(query) == 1 && (len(query["cursor"]) != 1 || query.Get("cursor") == "") || len(query.Get("cursor")) > 4096 {
			writeAnnotationError(w, http.StatusBadRequest, "annotation cursor is invalid")
			return
		}
		cursor, err := decodePDFAnnotationCursor(r.URL.Query().Get("cursor"), scope)
		if err != nil {
			writeAnnotationError(w, http.StatusBadRequest, "annotation cursor is invalid")
			return
		}
		page, err := s.workspaceStore.ListAnnotationPageOwned(r.Context(), workspace.AnnotationReadSource{Literature: &source}, cursor)
		if err != nil {
			writeAnnotationStoreError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, item.Public())
		}
		var next any
		if page.Next != nil {
			raw, _ := json.Marshal(pdfAnnotationCursor{Version: 1, Scope: scope, AnnotationPageCursor: *page.Next})
			next = base64.RawURLEncoding.EncodeToString(raw)
		}
		writeJSON(w, http.StatusOK, map[string]any{"target_key": "litv:" + version, "current_checksum": source.Checksum, "annotations": items, "total": page.Total, "next_cursor": next})
	case http.MethodPost:
		var input map[string]any
		if err := decodeAnnotationJSON(r, &input); err != nil {
			writeAnnotationError(w, http.StatusBadRequest, err.Error())
			return
		}
		checksum, _ := input["content_checksum"].(string)
		if checksum != source.Checksum {
			writeAnnotationError(w, http.StatusConflict, "PDF version changed; refresh before saving the annotation")
			return
		}
		delete(input, "content_checksum")
		body, err := normalizeAnnotationBody(input)
		if err != nil {
			writeAnnotationError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body["type"] != "point" && body["type"] != "text_selection" {
			writeAnnotationError(w, http.StatusBadRequest, "Library PDF annotations require a point or text selection")
			return
		}
		page, ok := body["page_number"].(float64)
		if !ok || page < 1 || page > float64(source.PageCount) {
			writeAnnotationError(w, http.StatusBadRequest, "a physical PDF page is required")
			return
		}
		if strings.TrimSpace(version) != version {
			writeAnnotationError(w, http.StatusBadRequest, "invalid PDF version")
			return
		}
		item, err := s.workspaceStore.CreateAnnotationOwned(r.Context(), workspace.CreateAnnotationInput{TargetKind: workspace.LiteratureAnnotationTargetKind,
			TargetKey: "litv:" + version, ContentChecksum: source.Checksum, Body: body}, owner)
		if err != nil {
			writeAnnotationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item.Public())
	default:
		w.Header().Set("Allow", "GET, POST")
		writeAnnotationError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
