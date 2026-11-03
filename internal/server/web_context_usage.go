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
	var readAuthority func() (*transcriptstore.ContextInspectionSnapshot, string, error)
	if inspect {
		readAuthority = func() (*transcriptstore.ContextInspectionSnapshot, string, error) {
			return s.contextInspectionAuthority(r.Context(), frame.ID)
		}
	}
	bundle, err := readContextUsageInspectionBundle(frame.ID, func() (runnerContextUsage, runnerContextHistory, bool, error) {
		return readRunnerContextUsage(s.runtimeStore, frame.ID)
	}, readAuthority)
	if err != nil {
		log.Printf("context_usage_read_failed frame=%s: %v", frame.ID, err)
		writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"message": "context usage temporarily unavailable"})
		return
	}
	if bundle.inspectionError != nil {
		writeContextInspectionError(w, bundle.inspectionError)
		return
	}
	result := map[string]any{"status": "unavailable"}
	if bundle.found && !bundle.legacy {
		result["status"], result["snapshot"], result["history"] = "available", bundle.snapshot, bundle.history
		policy, err := s.contextCompactionPolicy(bundle.snapshot.LimitTokens)
		if err != nil {
			log.Printf("context_usage_policy_unavailable frame=%s: %v", frame.ID, err)
		} else {
			result["autoCompaction"] = policy
		}
	}
	if inspect {
		result["inspection"] = nil
		if bundle.authority != nil {
			if err := s.transcriptStore.ValidateContextInspection(r.Context(), bundle.ownerID, bundle.authority.Scope); err != nil {
				writeContextInspectionError(w, err)
				return
			}
			result["inspection"] = projectContextInspection(*bundle.authority, bundle.history)
		}
	}
	writeWorkspaceJSON(w, http.StatusOK, result)
}

type contextUsageInspectionBundle struct {
	snapshot        runnerContextUsage
	history         runnerContextHistory
	found           bool
	legacy          bool
	authority       *transcriptstore.ContextInspectionSnapshot
	ownerID         string
	inspectionError error
}

// Freeze one owned runtime sample/request-ID set before the journal read.
// A later runtime write or retention rollover belongs to the next poll, never
// to a previously fixed journal association set. No Store lock crosses readers.
func readContextUsageInspectionBundle(sessionID string, readUsage func() (runnerContextUsage, runnerContextHistory, bool, error), readAuthority func() (*transcriptstore.ContextInspectionSnapshot, string, error)) (contextUsageInspectionBundle, error) {
	var result contextUsageInspectionBundle
	var err error
	result.snapshot, result.history, result.found, err = readUsage()
	result.legacy = errors.Is(err, errLegacyRunnerContextUsage) && result.snapshot.SessionID == sessionID
	if result.legacy {
		result.history = runnerContextHistory{SessionID: sessionID, TotalObserved: 1, Coverage: "latest_only", Samples: []runnerContextUsage{result.snapshot}}
	} else if err != nil {
		return result, err
	}
	if readAuthority != nil {
		result.authority, result.ownerID, result.inspectionError = readAuthority()
	}
	return result, nil
}

func writeContextInspectionError(w http.ResponseWriter, err error) {
	if errors.Is(err, transcriptstore.ErrBranchStateStale) || errors.Is(err, transcriptstore.ErrOwnerMismatch) {
		writeWorkspaceJSON(w, http.StatusConflict, map[string]any{"code": "context_scope_changed", "message": "context inspection scope changed; retry the read"})
		return
	}
	if errors.Is(err, transcriptstore.ErrEventConflict) {
		writeWorkspaceJSON(w, http.StatusConflict, map[string]any{"code": "context_snapshot_conflict", "message": "context inspection snapshot is incomplete; retry the read"})
		return
	}
	log.Printf("context_inspection_read_unavailable")
	writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"message": "context inspection temporarily unavailable"})
}
