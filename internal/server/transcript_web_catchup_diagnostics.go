package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"sync"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

type transcriptWebCatchupOwner string

const (
	transcriptWebCatchupRequest    transcriptWebCatchupOwner = "request"
	transcriptWebCatchupBackground transcriptWebCatchupOwner = "background"
)

type transcriptWebCatchupDiagnosticKey struct{}

// Flight telemetry follows the existing owner, independently of a canceled
// HTTP observer. It never supplies readiness or changes that owner's lifetime.
type transcriptWebCatchupDiagnostic struct {
	mu                    sync.Mutex
	id                    string
	owner                 transcriptWebCatchupOwner
	started, stageStarted time.Time
	stage                 transcriptWebReadStage
	mode                  string
	finished              bool
}

func newTranscriptWebCatchupDiagnostic(owner transcriptWebCatchupOwner) *transcriptWebCatchupDiagnostic {
	if owner != transcriptWebCatchupRequest && owner != transcriptWebCatchupBackground {
		return nil
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil // Entropy failure must not prevent the existing repair.
	}
	now := time.Now()
	return &transcriptWebCatchupDiagnostic{
		id: hex.EncodeToString(id[:]), owner: owner, started: now, stageStarted: now,
		stage: webReadCatchupLeader, mode: "none",
	}
}

func withTranscriptWebCatchupDiagnostic(ctx context.Context, diagnostic *transcriptWebCatchupDiagnostic) context.Context {
	if diagnostic == nil {
		return ctx
	}
	return context.WithValue(ctx, transcriptWebCatchupDiagnosticKey{}, diagnostic)
}

func noteTranscriptWebCatchupStage(ctx context.Context, stage transcriptWebReadStage) {
	diagnostic, _ := ctx.Value(transcriptWebCatchupDiagnosticKey{}).(*transcriptWebCatchupDiagnostic)
	if diagnostic == nil {
		return
	}
	switch stage {
	case webReadIncremental, webReadFull, webReadMetadata, webReadRecords, webReadCommit:
	default:
		return
	}
	diagnostic.mu.Lock()
	defer diagnostic.mu.Unlock()
	if !diagnostic.finished {
		diagnostic.stage, diagnostic.stageStarted = stage, time.Now()
		switch stage {
		case webReadIncremental:
			diagnostic.mode = "incremental"
		case webReadFull:
			diagnostic.mode = "full"
		}
	}
}

func (diagnostic *transcriptWebCatchupDiagnostic) finish(err error) {
	if diagnostic == nil {
		return
	}
	diagnostic.mu.Lock()
	if diagnostic.finished {
		diagnostic.mu.Unlock()
		return
	}
	diagnostic.finished = true
	id, owner, stage, mode := diagnostic.id, diagnostic.owner, diagnostic.stage, diagnostic.mode
	elapsed, stageElapsed := time.Since(diagnostic.started), time.Since(diagnostic.stageStarted)
	diagnostic.mu.Unlock()
	// "ok" describes only the owner's returned error, never a ready projection.
	log.Printf("history_catchup_flight flight_id=%s owner=%s stage=%s projection=%s outcome=%s elapsed_ms=%.3f stage_ms=%.3f",
		id, owner, stage, mode, transcriptWebCatchupOutcome(err),
		float64(elapsed.Microseconds())/1000, float64(stageElapsed.Microseconds())/1000)
}

func noteTranscriptWebCatchupWait(ctx context.Context, diagnostic *transcriptWebCatchupDiagnostic, started time.Time, err error) {
	if diagnostic == nil {
		return
	}
	requestID := "none"
	if request, _ := ctx.Value(transcriptWebReadDiagnosticKey{}).(*transcriptWebReadDiagnostic); request != nil {
		requestID = request.id // Assigned internally once, never supplied by a caller.
	}
	log.Printf("history_catchup_wait request_id=%s flight_id=%s owner=%s outcome=%s wait_ms=%.3f",
		requestID, diagnostic.id, diagnostic.owner, transcriptWebCatchupOutcome(err),
		float64(time.Since(started).Microseconds())/1000)
}

func transcriptWebCatchupOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, transcriptstore.ErrBranchStateStale):
		return "branch_stale"
	case errors.Is(err, transcriptstore.ErrTranscriptWebProjectionStale):
		return "projection_stale"
	default:
		return "failed"
	}
}
