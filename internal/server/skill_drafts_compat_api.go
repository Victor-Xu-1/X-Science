package server

import (
	"net/http"
	"os"
	"strings"
)

const compatibilitySkillDraftMarker = ".x-science-draft"

func (s *Server) handleCompatibilitySkillDrafts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if strings.TrimSpace(s.fileRoot) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"drafts": []string{}})
		return
	}
	drafts, err := s.compatibilitySkillDraftNames()
	if err != nil {
		writeV11Detail(w, http.StatusInternalServerError, "failed to list skill drafts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"drafts": drafts})
}

func regularDraftFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}
