package server

import (
	"context"
	"errors"
	"log"
	"strconv"
	"sync"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

const (
	contextCompactionArchiveSlice = 750 * time.Millisecond
	contextCompactionArchivePace  = time.Second
	contextCompactionArchiveRetry = 30 * time.Second
)

// Only the existing Transcript delivery coordinator owns this fixed-size
// navigation/backoff state. It is not durable execution or archive authority.
type contextCompactionArchiveRecovery struct {
	sync.Mutex
	cursor [2]struct {
		stream string
		event  int64
	}
	kind int
	next time.Time
}

// One background slice runs after realtime delivery. A missing projection never
// fails the coordinator, replays compaction, or changes input availability.
func (s *Server) repairContextCompactionArchivePass(parent context.Context, now time.Time) time.Duration {
	if s == nil || s.transcriptStore == nil || s.workspaceStore == nil || parent.Err() != nil {
		return 0
	}
	state := &s.contextCompactionArchives
	if !state.TryLock() {
		return contextCompactionArchivePace
	}
	defer state.Unlock()
	if now.Before(state.next) {
		return state.next.Sub(now)
	}
	// An empty scan does not arm an idle timer. A subsequent real wake inside
	// this cooldown returns its due delay above, so a new commit is not lost.
	state.next = now.Add(contextCompactionArchivePace)
	ctx, cancel := context.WithTimeout(parent, contextCompactionArchiveSlice)
	defer cancel()
	var work *transcriptstore.ContextCompactionArchiveWork
	var err error
	// Alternate real missing archives with already-written archives whose
	// durable notification is missing. Each category keeps its own cursor so
	// an old poison item cannot monopolize either bounded slot.
	for scanned := 0; scanned < 2; scanned++ {
		kind := (state.kind + scanned) % 2
		cursor := state.cursor[kind]
		work, err = s.transcriptStore.NextPendingContextCompactionArchive(ctx, kind == 0, cursor.stream, cursor.event)
		if work != nil {
			state.cursor[kind].stream, state.cursor[kind].event = work.StreamUID, work.BoundaryEventID
			state.kind = (kind + 1) % 2
		}
		if work != nil || err != nil {
			break
		}
	}
	if err == nil && work == nil {
		return 0
	}
	if err == nil {
		receipt, readErr := s.transcriptStore.ReadContextCompaction(ctx, work.OwnerID, work.FrameID, work.Request)
		err = readErr
		if err == nil {
			s.repairContextCompactionArchive(ctx, work.OwnerID, &receipt)
			// Publish only after a fresh durable read observes the real archive.
			receipt, err = s.transcriptStore.ReadContextCompaction(ctx, work.OwnerID, work.FrameID, work.Request)
			if err == nil && receipt.State == "committed" && receipt.ArchiveStatus == "available" {
				_, err = s.publishCompatEventContext(ctx, workspace.RealtimeEventInput{
					ID:     "context-compaction-archive:" + work.Request.RequestID + ":" + strconv.FormatInt(work.BoundaryEventID, 10),
					UserID: work.OwnerID, ProjectID: work.ProjectID, RootFrameID: work.Request.Expected.RootFrameID, FrameID: work.FrameID, Type: "compaction_status",
					Payload: map[string]any{"schema": "x-science.context-compaction-event.v1", "conversation_id": work.FrameID,
						"request_id": work.Request.RequestID, "frame_incarnation_id": work.Request.Expected.FrameIncarnationID,
						"root_frame_id": work.Request.Expected.RootFrameID, "root_frame_incarnation_id": work.Request.Expected.RootFrameIncarnationID,
						"state": receipt.State},
				})
				if err == nil {
					return contextCompactionArchivePace
				}
			} else if err == nil {
				err = errors.New("canonical archive projection is still pending")
			}
		}
	}
	if parent.Err() != nil {
		return 0
	}
	state.next = now.Add(contextCompactionArchiveRetry)
	log.Printf("context compaction archive recovery remains pending err_type=%T", err)
	return contextCompactionArchiveRetry
}
