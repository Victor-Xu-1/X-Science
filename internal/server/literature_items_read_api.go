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
		case "lifecycle":
			if values[0] != "active" && values[0] != "deleted" {
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return
			}
		case "reading_status":
			if values[0] == "" {
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return
			}
		case "sort":
			if query.Get("all_item_ids") == "true" {
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return
			}
			switch values[0] {
			case "title-asc", "title-desc", "updated-desc", "created-desc", "created-asc":
			default:
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return
			}
		case "order":
			if values[0] != "title-asc" || query.Get("all_item_ids") != "true" {
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
	filters := workspace.LiteratureQueryFilters{Query: query.Get("query"), ProjectID: query.Get("project_id"), CollectionID: query.Get("collection_id"), ReadingStatus: workspace.LiteratureReadingStatus(query.Get("reading_status")), Lifecycle: workspace.LiteratureLifecycle(query.Get("lifecycle"))}
	if query.Get("all_item_ids") == "true" {
		if query.Has("cursor") || query.Has("limit") {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		selection, err := s.workspaceStore.ListLiteratureItemIDsWithReadOptions(r.Context(), owner, filters, workspace.LiteratureSelectionReadOptions{Order: workspace.LiteratureSelectionReadOrder(query.Get("order"))})
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
	page, err := s.workspaceStore.ListLiteratureItemsInLifecycleWithReadOptions(r.Context(), owner, filters, query.Get("cursor"), limit, workspace.LiteraturePageReadOptions{Sort: workspace.LiteraturePageSort(query.Get("sort"))})
	if err != nil {
		writeLiteratureError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, page)
}
