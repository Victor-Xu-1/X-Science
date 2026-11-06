package server

import (
	"context"
	"errors"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// A request-scoped catch-up has priority over the global repair queue and must
// be long enough to rebuild a real, tool-heavy scientific conversation once.
// Concurrent readers share the same per-stream flight, so this bound does not
// multiply projection work.
const transcriptWebRequestCatchupTimeout = 15 * time.Second

type transcriptWebCatchupKey struct {
	ownerID   string
	streamUID string
	branchID  string
}

type transcriptWebCatchupFlight struct {
	done       chan struct{}
	err        error
	diagnostic *transcriptWebCatchupDiagnostic
}

func transcriptWebFenceReady(fence transcriptstore.TranscriptWebProjectionFence) bool {
	return fence.StateFound && fence.StateStatus == "ready" &&
		fence.StateProjectorVersion == transcriptstore.TranscriptWebProjectorVersion &&
		fence.StateBranchGeneration == fence.BranchGeneration &&
		fence.StateThroughPublicationSequence == fence.ThroughPublicationSequence &&
		fence.StateSourceRevision == fence.SourceRevision
}

func transcriptWebFenceCanCatchUp(fence transcriptstore.TranscriptWebProjectionFence) bool {
	// A newly-created conversation has no projection row yet. Treat that
	// bounded, exact-stream state as catch-up eligible so its first history read
	// and first realtime claim do not wait behind the global repair queue.
	return !fence.StateFound || fence.StateStatus == "ready" && !transcriptWebFenceReady(fence) ||
		fence.StateStatus == "quarantined" && fence.StateProjectorVersion >= 1 &&
			fence.StateProjectorVersion < transcriptstore.TranscriptWebProjectorVersion ||
		transcriptWebBuildingFenceCanCatchUp(fence)
}

func transcriptWebBuildingFenceCanCatchUp(fence transcriptstore.TranscriptWebProjectionFence) bool {
	return fence.StateFound && fence.StateStatus == "building" &&
		fence.StateProjectorVersion == transcriptstore.TranscriptWebProjectorVersion &&
		fence.StateBranchGeneration == fence.BranchGeneration &&
		fence.StateSourceRevision == fence.SourceRevision &&
		fence.StateThroughPublicationSequence > 0 &&
		fence.StateThroughPublicationSequence < fence.ThroughPublicationSequence
}

// catchUpTranscriptWebReadModel collapses concurrent reads for one stale
// conversation into one bounded rebuild. Different stream keys never share a
// flight, preserving task isolation and parallel progress.
func (s *Server) catchUpTranscriptWebReadModel(
	requestCtx context.Context,
	ownerID, streamUID, branchID string,
) error {
	key := transcriptWebCatchupKey{ownerID: ownerID, streamUID: streamUID, branchID: branchID}
	flight, leader := s.startTranscriptWebCatchupFlight(key, transcriptWebCatchupRequest)
	if !leader {
		noteTranscriptWebReadStage(requestCtx, webReadCatchupWait)
		started := time.Now()
		var err error
		select {
		case <-flight.done:
			err = flight.err
		case <-requestCtx.Done():
			err = requestCtx.Err()
		}
		noteTranscriptWebCatchupWait(requestCtx, flight.diagnostic, started, err)
		return err
	}

	noteTranscriptWebReadStage(requestCtx, webReadCatchupLeader)
	catchupCtx, cancel := context.WithTimeout(context.WithoutCancel(requestCtx), transcriptWebRequestCatchupTimeout)
	catchupCtx = withTranscriptWebCatchupDiagnostic(catchupCtx, flight.diagnostic)
	err := s.rebuildOneStaleTranscriptWebReadModel(catchupCtx, key)
	cancel()
	s.finishTranscriptWebCatchupFlight(key, flight, err)
	return err
}

// Request repair and the existing background coordinator share one stream
// owner. The raw projector never acquires a flight, avoiding reentrant waits.
func (s *Server) startTranscriptWebCatchupFlight(key transcriptWebCatchupKey, owner transcriptWebCatchupOwner) (*transcriptWebCatchupFlight, bool) {
	// Entropy and diagnostic allocation stay outside the shared flight-map lock.
	diagnostic := newTranscriptWebCatchupDiagnostic(owner)
	s.transcriptWebCatchupMu.Lock()
	defer s.transcriptWebCatchupMu.Unlock()
	if s.transcriptWebCatchups == nil {
		s.transcriptWebCatchups = make(map[transcriptWebCatchupKey]*transcriptWebCatchupFlight)
	}
	if flight := s.transcriptWebCatchups[key]; flight != nil {
		return flight, false
	}
	flight := &transcriptWebCatchupFlight{done: make(chan struct{}), diagnostic: diagnostic}
	s.transcriptWebCatchups[key] = flight
	return flight, true
}

func (s *Server) finishTranscriptWebCatchupFlight(key transcriptWebCatchupKey, flight *transcriptWebCatchupFlight, err error) {
	s.transcriptWebCatchupMu.Lock()
	flight.err = err
	delete(s.transcriptWebCatchups, key)
	close(flight.done)
	s.transcriptWebCatchupMu.Unlock()
	flight.diagnostic.finish(err)
}

func (s *Server) rebuildOneStaleTranscriptWebReadModel(
	ctx context.Context,
	key transcriptWebCatchupKey,
) error {
	work, found, err := s.transcriptWebReadModel.GetTranscriptWebProjectionWork(
		ctx, key.ownerID, key.streamUID, key.branchID,
	)
	if err != nil || !found {
		return err
	}
	upgrade := work.Reason == "projection_projector_version_stale"
	if work.ProjectionStatus == "building" {
		if upgrade {
			return nil
		}
		stream, err := s.transcriptStore.GetStream(ctx, work.StreamUID, work.OwnerID)
		if err != nil {
			return err
		}
		if stream.SessionID != work.SessionID || stream.Kind != transcriptstore.StreamKindFrameRef {
			return transcriptstore.ErrEventConflict
		}
		// A request may resume one verified partial append, never turn an
		// invalid or unsupported building checkpoint into a full rebuild.
		result, err := s.tryIncrementalTranscriptWebReadModel(ctx, stream, work)
		if err != nil {
			return err
		}
		if !result.applied() {
			return transcriptstore.ErrTranscriptWebProjectionStale
		}
		return nil
	}
	eligible := work.Status == transcriptstore.TranscriptWebProjectionWorkMissing ||
		work.ProjectionStatus == "quarantined" && upgrade ||
		work.ProjectionStatus == "ready" &&
			(work.Status == transcriptstore.TranscriptWebProjectionWorkDirty ||
				work.Status == transcriptstore.TranscriptWebProjectionWorkStale)
	if !eligible {
		return nil
	}
	if err := s.rebuildTranscriptWebReadModel(ctx, work); err != nil {
		if errors.Is(err, transcriptstore.ErrBranchStateStale) ||
			errors.Is(err, transcriptstore.ErrTranscriptWebProjectionStale) {
			fence, fenceErr := s.transcriptWebReadModel.GetTranscriptWebProjectionFence(
				ctx, key.ownerID, key.streamUID, key.branchID,
			)
			if fenceErr == nil && transcriptWebFenceReady(fence) {
				return nil
			}
		}
		if !transcriptWebProjectionRepairInterrupted(err) {
			// Settle deterministic failures through the background repair's same
			// quarantine authority, including failures in the current projector.
			// Repeated reads must not retry unchanged invalid source indefinitely.
			if quarantineErr := s.quarantineTranscriptWebReadModel(ctx, work, err); quarantineErr != nil {
				return errors.Join(err, quarantineErr)
			}
			s.noteTranscriptWebProjectionRetry(work.StreamUID)
			return transcriptstore.ErrTranscriptWebProjectionStale // Reload the settled fence.
		}
		return err
	}
	return nil
}
