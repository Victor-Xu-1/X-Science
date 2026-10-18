package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) routeSessionPermissionGrants(w http.ResponseWriter, r *http.Request) bool {
	segments := workspacePathSegments(strings.TrimPrefix(r.URL.Path, "/api/conversations/"))
	if len(segments) < 2 || segments[1] != "permission-grants" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	owner := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	if owner == "" {
		sessionGrantAPIError(w, http.StatusUnauthorized, "AUTH_REQUIRED")
		return true
	}
	frameID, err := url.PathUnescape(segments[0])
	if err != nil || strings.TrimSpace(frameID) == "" {
		writeSessionGrantError(w, workspace.ErrSessionGrantRequestInvalid)
		return true
	}
	if s.workspaceStore == nil {
		writeSessionGrantError(w, errors.New("workspace unavailable"))
		return true
	}
	if len(segments) == 2 && r.Method == http.MethodGet {
		expected, err := sessionGrantExpectedQuery(r.URL.Query())
		if err != nil {
			writeSessionGrantError(w, err)
			return true
		}
		snapshot, err := s.workspaceStore.SessionPermissionGrants(r.Context(), owner, frameID, expected)
		if err != nil {
			writeSessionGrantError(w, err)
			return true
		}
		writeWorkspaceJSON(w, http.StatusOK, snapshot)
		return true
	}
	if len(segments) == 3 && segments[2] == "revoke" && r.Method == http.MethodPost {
		if len(r.URL.Query()) != 0 {
			writeSessionGrantError(w, workspace.ErrSessionGrantRequestInvalid)
			return true
		}
		var input struct {
			Schema string `json:"schema"`
			workspace.SessionPermissionExpectedScope
			Grants []workspace.SessionPermissionGrantSelection `json:"grants"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var trailing any
		if err := decoder.Decode(&input); err != nil || input.Schema != "x-science.session-permission-revoke.v1" || decoder.Decode(&trailing) != io.EOF {
			writeSessionGrantError(w, workspace.ErrSessionGrantRequestInvalid)
			return true
		}
		result, err := s.workspaceStore.RevokeSessionPermissionGrants(r.Context(), workspace.SessionPermissionGrantRevokeInput{
			OwnerID: owner, FrameID: frameID, Expected: input.SessionPermissionExpectedScope, Grants: input.Grants,
		})
		if err != nil {
			writeSessionGrantError(w, err)
			return true
		}
		writeWorkspaceJSON(w, http.StatusOK, result)
		return true
	}
	sessionGrantAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	return true
}

func sessionGrantExpectedQuery(query url.Values) (*workspace.SessionPermissionExpectedScope, error) {
	if len(query) == 0 {
		return nil, nil
	}
	keys := []string{"expected_frame_incarnation_id", "expected_root_frame_id", "expected_root_frame_incarnation_id"}
	if len(query) != len(keys) {
		return nil, workspace.ErrSessionGrantRequestInvalid
	}
	for _, key := range keys {
		values := query[key]
		if len(values) != 1 || values[0] == "" || len(values[0]) > 512 {
			return nil, workspace.ErrSessionGrantRequestInvalid
		}
	}
	return &workspace.SessionPermissionExpectedScope{FrameIncarnationID: query.Get(keys[0]), RootFrameID: query.Get(keys[1]), RootFrameIncarnationID: query.Get(keys[2])}, nil
}

func writeSessionGrantError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "SESSION_GRANT_STORAGE_UNAVAILABLE"
	switch {
	case errors.Is(err, workspace.ErrSessionGrantTargetUnavailable):
		status, code = http.StatusNotFound, "SESSION_GRANT_TARGET_UNAVAILABLE"
	case errors.Is(err, workspace.ErrSessionGrantScopeStale):
		status, code = http.StatusConflict, "SESSION_GRANT_SCOPE_STALE"
	case errors.Is(err, workspace.ErrSessionGrantRequestInvalid):
		status, code = http.StatusBadRequest, "SESSION_GRANT_REQUEST_INVALID"
	}
	sessionGrantAPIError(w, status, code)
}

func sessionGrantAPIError(w http.ResponseWriter, status int, code string) {
	writeWorkspaceJSON(w, status, map[string]any{"code": code, "message": "Session permissions could not be updated."})
}

func writeVersionedSessionGrantRequired(w http.ResponseWriter) {
	writeWorkspaceJSON(w, http.StatusConflict, map[string]any{
		"code":        "SESSION_GRANT_VERSIONED_REVOKE_REQUIRED",
		"message":     "Use the session permission endpoint with the exact observed grant generations.",
		"replacement": "/api/conversations/{frame}/permission-grants/revoke",
	})
}
