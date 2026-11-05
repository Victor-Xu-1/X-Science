package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type transcriptWebReadStage string

const (
	webReadAuthentication transcriptWebReadStage = "authentication"
	webReadIdentity       transcriptWebReadStage = "identity"
	webReadAccess         transcriptWebReadStage = "access"
	webReadFence          transcriptWebReadStage = "authority_fence"
	webReadCatchupLeader  transcriptWebReadStage = "catchup_leader_lookup"
	webReadCatchupWait    transcriptWebReadStage = "catchup_follower_wait"
	webReadIncremental    transcriptWebReadStage = "project_incremental"
	webReadFull           transcriptWebReadStage = "project_full"
	webReadMetadata       transcriptWebReadStage = "project_metadata"
	webReadRecords        transcriptWebReadStage = "project_records"
	webReadCommit         transcriptWebReadStage = "project_commit"
	webReadPage           transcriptWebReadStage = "page_read"
	webReadSanitize       transcriptWebReadStage = "page_sanitize"
	webReadEnrich         transcriptWebReadStage = "page_enrich"
	webReadDetail         transcriptWebReadStage = "detail_source"
	webReadResponse       transcriptWebReadStage = "response"
)

type transcriptWebReadDiagnosticKey struct{}

// This is request-local telemetry, never a readiness or repair authority. The
// original request context remains here even when shared repair detaches its
// cancellation; cancellation must describe the blocked request's current stage.
type transcriptWebReadDiagnostic struct {
	mu           sync.Mutex
	ctx          context.Context
	id, route    string
	started      time.Time
	stageStarted time.Time
	stage        transcriptWebReadStage
	role, mode   string
	finished     bool
}

func transcriptWebReadDiagnosticRoute(r *http.Request) string {
	if r == nil || r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/conversations/") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/conversations/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != "messages" {
		return ""
	}
	if len(parts) == 2 {
		return "history_page"
	}
	if len(parts) == 3 && parts[2] != "" || len(parts) == 4 && parts[2] != "" && parts[3] == "tool-detail" {
		return "history_detail"
	}
	return ""
}

func withTranscriptWebReadDiagnostics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := transcriptWebReadDiagnosticRoute(r)
		if route == "" {
			next.ServeHTTP(w, r)
			return
		}
		var randomID [8]byte
		if _, err := rand.Read(randomID[:]); err != nil {
			// Diagnostics must not turn an entropy failure into a read failure.
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		diagnostic := &transcriptWebReadDiagnostic{
			ctx: r.Context(), id: hex.EncodeToString(randomID[:]), route: route,
			started: started, stageStarted: started, stage: webReadAuthentication,
			role: "none", mode: "none",
		}
		stop := context.AfterFunc(r.Context(), diagnostic.finish)
		defer func() {
			stop()
			diagnostic.finish()
		}()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), transcriptWebReadDiagnosticKey{}, diagnostic)))
	})
}

func noteTranscriptWebReadStage(ctx context.Context, stage transcriptWebReadStage) {
	if ctx == nil {
		return
	}
	if diagnostic, _ := ctx.Value(transcriptWebReadDiagnosticKey{}).(*transcriptWebReadDiagnostic); diagnostic != nil {
		diagnostic.mu.Lock()
		if diagnostic.ctx.Err() != nil {
			diagnostic.mu.Unlock()
			// Do not let a late detached repair overwrite the stage observed at
			// request cancellation before AfterFunc gets scheduled.
			diagnostic.finish()
			return
		}
		if !diagnostic.finished {
			diagnostic.stage, diagnostic.stageStarted = stage, time.Now()
			switch stage {
			case webReadCatchupLeader:
				diagnostic.role = "leader"
			case webReadCatchupWait:
				diagnostic.role = "follower"
			case webReadIncremental:
				diagnostic.mode = "incremental"
			case webReadFull:
				diagnostic.mode = "full"
			}
		}
		diagnostic.mu.Unlock()
	}
}

func (diagnostic *transcriptWebReadDiagnostic) finish() {
	diagnostic.mu.Lock()
	if diagnostic.finished {
		diagnostic.mu.Unlock()
		return
	}
	diagnostic.finished = true
	elapsed := time.Since(diagnostic.started)
	outcome := "completed"
	switch diagnostic.ctx.Err() {
	case context.Canceled:
		outcome = "canceled"
	case context.DeadlineExceeded:
		outcome = "deadline"
	}
	if outcome == "completed" && elapsed < time.Second {
		diagnostic.mu.Unlock()
		return
	}
	id, route, stage := diagnostic.id, diagnostic.route, diagnostic.stage
	role, mode := diagnostic.role, diagnostic.mode
	stageElapsed := time.Since(diagnostic.stageStarted)
	diagnostic.mu.Unlock()
	// Every string is either generated here or a fixed internal label. Never
	// include URL, caller request IDs, identity, query, error, or payload data.
	log.Printf("history_read_request request_id=%s route=%s stage=%s outcome=%s elapsed_ms=%.3f stage_ms=%.3f catchup_role=%s projection=%s",
		id, route, stage, outcome,
		float64(elapsed.Microseconds())/1000, float64(stageElapsed.Microseconds())/1000,
		role, mode)
}
