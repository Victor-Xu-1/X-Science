package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func (s *Server) registerNotificationInboxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/notifications/inbox", s.handleNotificationInbox)
	mux.HandleFunc("/api/notifications/inbox/", s.handleNotificationInbox)
}

func (s *Server) handleNotificationInbox(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	if owner == "" {
		writeWorkspaceJSON(w, http.StatusUnauthorized, map[string]any{"code": "AUTH_REQUIRED"})
		return
	}
	if s.runtimeStore == nil || s.workspaceStore == nil || s.transcriptStore == nil {
		writeNotificationInboxError(w, errors.New("notification storage unavailable"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/notifications/inbox")
	var result notificationInboxSnapshot
	var err error
	switch {
	case path == "" && r.Method == http.MethodGet:
		query := r.URL.Query()
		for key, values := range query {
			if key != "cursor" || len(values) != 1 || len(values[0]) > 512 {
				writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"code": "INVALID_INBOX_QUERY"})
				return
			}
		}
		result, err = s.reconcileNotificationInbox(r.Context(), owner, query.Get("cursor"))
	case (path == "/read" || path == "/read-all") && r.Method == http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var ids []string
		var receipt string
		if path == "/read" {
			var body struct {
				IDs []string `json:"ids"`
			}
			err = decoder.Decode(&body)
			ids = body.IDs
			if len(ids) < 1 || len(ids) > 1200 {
				err = errors.New("invalid notification IDs")
			}
			for _, id := range ids {
				if id == "" || len(id) > 128 {
					err = errors.New("invalid notification ID")
				}
			}
		} else {
			var body struct {
				ReadReceipt string `json:"read_receipt"`
			}
			err = decoder.Decode(&body)
			receipt = body.ReadReceipt
			if receipt == "" || len(receipt) > 512 {
				err = errors.New("invalid read receipt")
			}
		}
		var trailing any
		if err != nil || decoder.Decode(&trailing) != io.EOF {
			writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"code": "INVALID_INBOX_REQUEST"})
			return
		}
		result, err = s.markNotificationInboxRead(owner, ids, receipt)
	default:
		if path != "" && path != "/read" && path != "/read-all" {
			http.NotFound(w, r)
			return
		}
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "METHOD_NOT_ALLOWED"})
		return
	}
	if err != nil {
		writeNotificationInboxError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, result)
}

func writeNotificationInboxError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "NOTIFICATION_INBOX_UNAVAILABLE"
	if errors.Is(err, errNotificationInboxConflict) {
		status, code = http.StatusConflict, "NOTIFICATION_INBOX_CONFLICT"
	}
	writeWorkspaceJSON(w, status, map[string]any{"code": code, "message": "Notification inbox could not be updated."})
}
