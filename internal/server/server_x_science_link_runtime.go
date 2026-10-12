package server

import (
	"encoding/json"
	"errors"

	"net/http"

	"os"

	"path/filepath"

	"strconv"
	"strings"

	"x-science/internal/xsciencelink"
)

func (s *Server) handleXScienceLink(w http.ResponseWriter, r *http.Request) {
	tail := r.URL.Path[len("/api/x-science-link"):]
	if tail == "" || tail == "/" || tail == "/clients" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "X-Science Link clients only supports GET")
			return
		}
		userID := resolveUserID(r, nil)
		writeJSON(w, http.StatusOK, map[string]any{
			"clients": s.xscienceLink.ListClients(userID),
		})
		return
	}

	if tail == "/capabilities" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "X-Science Link capabilities only supports GET")
			return
		}
		defaults := s.xscienceLink.PolicyDefaults()
		writeJSON(w, http.StatusOK, map[string]any{
			"protocolVersion": "1",
			"policyVersion":   "2",
			"policyDefaults":  defaults,
			"actions":         xsciencelink.ActionProfilesWithDefaults(defaults),
		})
		return
	}

	if tail == "/doctor" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "X-Science Link doctor only supports GET")
			return
		}
		s.handleXScienceLinkDoctor(w, r)
		return
	}

	if tail == "/inspect" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "X-Science Link inspect only supports GET")
			return
		}
		s.handleXScienceLinkInspect(w, r)
		return
	}

	if tail == "/running" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "X-Science Link running only supports GET")
			return
		}
		userID := resolveUserID(r, nil)
		writeJSON(w, http.StatusOK, map[string]any{
			"running": s.xscienceLink.ListRunning(userID),
		})
		return
	}

	if tail == "/logs" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "X-Science Link logs only supports GET")
			return
		}
		userID := resolveUserID(r, nil)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		writeJSON(w, http.StatusOK, map[string]any{
			"logs": s.xscienceLink.ListTaskLogs(userID, limit),
		})
		return
	}

	if tail == "/access-requests" {
		s.handleXScienceLinkAccessRequests(w, r)
		return
	}

	if strings.HasPrefix(tail, "/access-requests/") {
		s.handleXScienceLinkAccessRequest(w, r, strings.TrimPrefix(tail, "/access-requests/"))
		return
	}

	if strings.HasPrefix(tail, "/devices/") {
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "X-Science Link device revoke only supports DELETE")
			return
		}
		userID := resolveUserID(r, nil)
		deviceID := strings.TrimPrefix(tail, "/devices/")
		if userID == "" || deviceID == "" {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "userId and deviceId are required")
			return
		}
		if !s.xscienceLink.RevokeDevice(userID, deviceID, "api revoked") {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "X-Science Link device not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deviceId": deviceID})
		return
	}

	if tail == "/ws" {
		s.handleXScienceLinkWebSocket(w, r)
		return
	}

	if tail == "/commands" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "X-Science Link commands only supports POST")
			return
		}
		s.handleXScienceLinkCommand(w, r)
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "Unknown X-Science Link route")
}

func (s *Server) handleXScienceLinkInspect(w http.ResponseWriter, r *http.Request) {
	userID := resolveUserID(r, nil)
	clientID := strings.TrimSpace(r.URL.Query().Get("clientId"))
	if userID == "" || clientID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "userId and clientId are required")
		return
	}
	var client xsciencelink.Client
	found := false
	for _, candidate := range s.xscienceLink.ListClients(userID) {
		if candidate.ID == clientID {
			client = candidate
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "X-Science Link client not found")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	defaults := s.xscienceLink.PolicyDefaults()
	running := filterRunningByClient(s.xscienceLink.ListRunning(userID), clientID)
	logs := filterTaskLogsByClient(s.xscienceLink.ListTaskLogs(userID, limit), clientID, limit)
	accessRequests := filterAccessRequestsByClient(s.xscienceLink.ListAccessRequests(userID, ""), clientID)
	supported, blocked := inspectClientActions(client, defaults)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"client":         client,
		"running":        running,
		"logs":           logs,
		"accessRequests": accessRequests,
		"policyDefaults": defaults,
		"actions": map[string]any{
			"supported": supported,
			"blocked":   blocked,
		},
		"summary": map[string]any{
			"running":        len(running),
			"logs":           len(logs),
			"accessRequests": len(accessRequests),
			"supported":      len(supported),
			"blocked":        len(blocked),
		},
	})
}

func (s *Server) handleXScienceLinkDoctor(w http.ResponseWriter, r *http.Request) {
	userID := resolveUserID(r, nil)
	clients := s.xscienceLink.ListClients(userID)
	defaults := s.xscienceLink.PolicyDefaults()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"report": map[string]any{
			"server": map[string]any{
				"protocolVersion": "1",
				"policyVersion":   "1",
				"apiAuth":         "userId",
			},
			"clients":        clients,
			"running":        s.xscienceLink.ListRunning(userID),
			"logs":           s.xscienceLink.ListTaskLogs(userID, 50),
			"warnings":       xscienceLinkWarnings(clients),
			"policyDefaults": defaults,
			"rememberedApprovals": map[string]any{
				"count": s.rememberedApprovalDecisionCount(),
				"key":   approvalRememberedSettingKey,
			},
			"policyMatrix": xsciencelink.PolicyMatrixWithDefaults(defaults),
		},
	})
}

func (s *Server) handleXScienceLinkCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID   string         `json:"userId"`
		ClientID string         `json:"clientId"`
		Name     string         `json:"name"`
		Payload  map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON body")
		return
	}
	userID := resolveUserID(r, map[string]any{"userId": body.UserID})
	if userID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "userId is required")
		return
	}
	if body.ClientID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "clientId is required")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
		return
	}

	command, resultCh, err := s.xscienceLink.SendCommand(r.Context(), userID, body.ClientID, body.Name, body.Payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	select {
	case result := <-resultCh:
		if !result.OK {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", result.Error)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"commandId": command.ID,
			"result":    result.Value,
		})
	case <-r.Context().Done():
		writeError(w, http.StatusRequestTimeout, "REQUEST_TIMEOUT", "X-Science Link command request was cancelled")
	}
}

func filterRunningByClient(items []xsciencelink.RunningTask, clientID string) []xsciencelink.RunningTask {
	filtered := make([]xsciencelink.RunningTask, 0)
	for _, item := range items {
		if item.ClientID == clientID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func filterTaskLogsByClient(items []xsciencelink.TaskLog, clientID string, limit int) []xsciencelink.TaskLog {
	filtered := make([]xsciencelink.TaskLog, 0)
	for _, item := range items {
		if item.ClientID != clientID {
			continue
		}
		filtered = append(filtered, item)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func filterAccessRequestsByClient(items []xsciencelink.AccessRequest, clientID string) []xsciencelink.AccessRequest {
	filtered := make([]xsciencelink.AccessRequest, 0)
	for _, item := range items {
		if item.ClientID == clientID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func inspectClientActions(client xsciencelink.Client, defaults xsciencelink.PolicyDefaults) ([]map[string]any, []map[string]any) {
	supported := make([]map[string]any, 0)
	blocked := make([]map[string]any, 0)
	for _, profile := range xsciencelink.ActionProfilesWithDefaults(defaults) {
		if client.Kind != "" && profile.ClientKind != "" && client.Kind != profile.ClientKind {
			continue
		}
		reasons := actionBlockReasons(client, profile)
		record := map[string]any{
			"action":                  profile.Action,
			"family":                  profile.Family,
			"readOnly":                profile.ReadOnly,
			"approvalPolicy":          profile.ApprovalPolicy,
			"effectiveApprovalPolicy": profile.EffectiveApprovalPolicy,
			"capabilities":            profile.Capabilities,
		}
		if len(reasons) == 0 {
			supported = append(supported, record)
		} else {
			record["reasons"] = reasons
			blocked = append(blocked, record)
		}
	}
	return supported, blocked
}

func actionBlockReasons(client xsciencelink.Client, profile xsciencelink.ActionProfile) []string {
	reasons := make([]string, 0)
	if len(client.SupportedActions) > 0 && !stringSliceContains(client.SupportedActions, profile.Action) {
		reasons = append(reasons, "not advertised in supportedActions")
	}
	for _, capability := range profile.Capabilities {
		if !stringSliceContains(client.Capabilities, capability) {
			reasons = append(reasons, "missing capability "+capability)
		}
	}
	return reasons
}

func (s *Server) handleXScienceLinkDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "X-Science Link download only supports GET and HEAD")
		return
	}
	if s.linkPackagePath == "" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "X-Science Link extension package is not configured")
		return
	}
	info, err := os.Stat(s.linkPackagePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "X-Science Link extension package is not available")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "X-Science Link extension package could not be read")
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "X-Science Link extension package is not available")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="x-science-browser-extension.zip"`)
	w.Header().Set("Content-Length", stringInt(info.Size()))
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.ServeFile(w, r, filepath.Clean(s.linkPackagePath))
}
