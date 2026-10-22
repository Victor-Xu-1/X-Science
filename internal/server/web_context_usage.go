package server

import (
	"errors"
	"log"
	"net/http"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// Access is checked by handleWebConversation before this read-only endpoint.
func (s *Server) handleWebContextUsage(w http.ResponseWriter, r *http.Request, frame workspace.CompatibilityFrame) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "method not allowed"})
		return
	}
	if s.runtimeStore == nil {
		writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"message": "context usage storage unavailable"})
		return
	}
	inspect := r.URL.Query().Get("inspect") == "1"
	var authority *transcriptstore.ContextInspectionSnapshot
	var ownerID string
	if inspect {
		var err error
		authority, ownerID, err = s.contextInspectionAuthority(r.Context(), frame.ID)
		if err != nil {
			writeContextInspectionError(w, err)
			return
		}
	}
	snapshot, history, found, err := readRunnerContextUsage(s.runtimeStore, frame.ID)
	legacy := errors.Is(err, errLegacyRunnerContextUsage) && snapshot.SessionID == frame.ID
	if legacy {
		history = runnerContextHistory{SessionID: frame.ID, TotalObserved: 1, Coverage: "latest_only", Samples: []runnerContextUsage{snapshot}}
	}
	if err != nil && !legacy {
		log.Printf("context_usage_read_failed frame=%s: %v", frame.ID, err)
		writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"message": "context usage temporarily unavailable"})
		return
	}
	result := map[string]any{"status": "unavailable"}
	if found && !legacy {
		result["status"], result["snapshot"], result["history"] = "available", snapshot, history
		policy, err := s.contextCompactionPolicy(snapshot.LimitTokens)
		if err != nil {
			log.Printf("context_usage_policy_unavailable frame=%s: %v", frame.ID, err)
		} else {
			result["autoCompaction"] = policy
		}
	}
	if inspect {
		result["inspection"] = nil
		if authority != nil {
			if err := s.transcriptStore.ValidateContextInspection(r.Context(), ownerID, authority.Scope); err != nil {
				writeContextInspectionError(w, err)
				return
			}
			result["inspection"] = projectContextInspection(*authority, history)
		}
	}
	writeWorkspaceJSON(w, http.StatusOK, result)
}

func writeContextInspectionError(w http.ResponseWriter, err error) {
	if errors.Is(err, transcriptstore.ErrBranchStateStale) || errors.Is(err, transcriptstore.ErrEventConflict) {
		writeWorkspaceJSON(w, http.StatusConflict, map[string]any{"message": "context inspection scope changed; retry the read"})
		return
	}
	log.Printf("context_inspection_read_unavailable")
	writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"message": "context inspection temporarily unavailable"})
}
