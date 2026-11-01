package server

import (
	"errors"
	"net/http"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

func writeResourceTagError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "RESOURCE_TAG_INTERNAL"
	switch {
	case errors.Is(err, workspace.ErrResourceTagInvalid):
		status, code = http.StatusBadRequest, "RESOURCE_TAG_INVALID"
	case errors.Is(err, workspace.ErrResourceTagUnavailable):
		status, code = http.StatusNotFound, "RESOURCE_TAG_UNAVAILABLE"
	case errors.Is(err, workspace.ErrResourceTagProtected):
		status, code = http.StatusConflict, "RESOURCE_TAG_PROTECTED"
	case errors.Is(err, workspace.ErrResourceTagNameConflict):
		status, code = http.StatusConflict, "RESOURCE_TAG_NAME_CONFLICT"
	case errors.Is(err, workspace.ErrResourceTagConflict):
		status, code = http.StatusConflict, "RESOURCE_TAG_CONFLICT"
	case errors.Is(err, workspace.ErrMutationIdempotencyConflict):
		status, code = http.StatusConflict, "RESOURCE_TAG_OPERATION_CONFLICT"
	}
	writeWorkspaceJSON(w, status, map[string]string{"code": code})
}

func writeResourceTagResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeResourceTagError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, value)
}

// Account identity comes only from the existing authenticated HTTP boundary.
// Catalog mutations and their receipts use the canonical workspace transaction.
func (s *Server) handleResourceTags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, ok := attachmentUserID(w, r)
	if !ok {
		return
	}
	if s.workspaceStore == nil {
		writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "RESOURCE_TAG_UNAVAILABLE"})
		return
	}
	if len(r.URL.Query()) != 0 {
		writeResourceTagError(w, workspace.ErrResourceTagInvalid)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/tags")
	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodGet:
			value, err := s.workspaceStore.ListResourceTags(r.Context(), owner)
			writeResourceTagResult(w, value, err)
		case http.MethodPost:
			var input workspace.ResourceTagCreate
			if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
				writeResourceTagError(w, workspace.ErrResourceTagInvalid)
				return
			}
			value, err := s.workspaceStore.CreateResourceTag(r.Context(), owner, input)
			writeResourceTagResult(w, value, err)
		default:
			w.Header().Set("Allow", "GET, POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if path == "/order" && r.Method == http.MethodPut {
		var input workspace.ResourceTagOrder
		if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
			writeResourceTagError(w, workspace.ErrResourceTagInvalid)
			return
		}
		value, err := s.workspaceStore.ReorderResourceTags(r.Context(), owner, input)
		writeResourceTagResult(w, value, err)
		return
	}
	if path == "/assignments" && r.Method == http.MethodPut {
		var input struct {
			OperationID  string `json:"operation_id"`
			TagID        string `json:"tag_id"`
			ResourceType string `json:"resource_type"`
			ResourceID   string `json:"resource_id"`
			Assigned     *bool  `json:"assigned"`
		}
		if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) || input.Assigned == nil {
			writeResourceTagError(w, workspace.ErrResourceTagInvalid)
			return
		}
		value, err := s.workspaceStore.SetResourceTagAssignment(r.Context(), owner, workspace.ResourceTagSetAssignment{
			OperationID: input.OperationID, TagID: input.TagID, ResourceType: input.ResourceType, ResourceID: input.ResourceID, Assigned: *input.Assigned,
		})
		writeResourceTagResult(w, value, err)
		return
	}
	id := strings.TrimPrefix(path, "/")
	if id == "" || strings.Contains(id, "/") || id == "order" || id == "assignments" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var input workspace.ResourceTagUpdate
		if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
			writeResourceTagError(w, workspace.ErrResourceTagInvalid)
			return
		}
		value, err := s.workspaceStore.UpdateResourceTag(r.Context(), owner, id, input)
		writeResourceTagResult(w, value, err)
	case http.MethodDelete:
		var input struct {
			OperationID string `json:"operation_id"`
		}
		if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
			writeResourceTagError(w, workspace.ErrResourceTagInvalid)
			return
		}
		value, err := s.workspaceStore.DeleteResourceTag(r.Context(), owner, id, input.OperationID)
		writeResourceTagResult(w, value, err)
	default:
		w.Header().Set("Allow", "PATCH, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
