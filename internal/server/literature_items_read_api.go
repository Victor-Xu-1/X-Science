package server

import (
	"net/http"
	"strconv"

	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) handleLiteratureItemsRead(w http.ResponseWriter, r *http.Request, owner string) {
	query := r.URL.Query()
	for key, values := range query {
		if len(values) != 1 {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		switch key {
		case "query", "project_id", "collection_id", "cursor", "limit":
		case "reading_status":
			if values[0] == "" {
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return
			}
		case "all_item_ids":
			if values[0] != "true" && values[0] != "false" {
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return
			}
		default:
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
	}
	if query.Get("all_item_ids") == "true" {
		if query.Has("cursor") || query.Has("limit") {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		selection, err := s.workspaceStore.ListLiteratureItemIDs(r.Context(), owner, query.Get("query"), query.Get("project_id"), query.Get("collection_id"), workspace.LiteratureReadingStatus(query.Get("reading_status")))
		if err != nil {
			writeLiteratureError(w, err)
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, selection)
		return
	}
	limit := 20
	var err error
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
	}
	if err != nil {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	page, err := s.workspaceStore.ListLiteratureItemsWithReadingStatus(r.Context(), owner, query.Get("query"), query.Get("project_id"), query.Get("collection_id"), workspace.LiteratureReadingStatus(query.Get("reading_status")), query.Get("cursor"), limit)
	if err != nil {
		writeLiteratureError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, page)
}
