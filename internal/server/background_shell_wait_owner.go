package server

import (
	"context"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/tools/shellops"
)

// This extends the existing command lifetime registration, not task metadata:
// TaskUpdate metadata is model-writable and cannot confer execution ownership.
type backgroundShellEntry struct {
	*shellops.RunningCommand
	owner *backgroundShellOwner
}

type backgroundShellOwner struct {
	UserID, ProjectID, FrameID, FrameIncarnationID string
	RootFrameID, RootFrameIncarnationID, BranchID  string
}

type taskOutputWaitObservation struct{ confirmed bool }
type taskOutputWaitObservationKey struct{}

// A current live claim authorizes this observation, but the lifetime owner uses
// stable frame/root/branch identity. Attempts, tokens and input revisions can
// legitimately change while the same background command continues running.
func (s *Server) currentBackgroundShellOwner(ctx context.Context) *backgroundShellOwner {
	if s == nil || s.workspaceStore == nil || s.transcriptStore == nil || ctx == nil || ctx.Err() != nil {
		return nil
	}
	run, _ := ctx.Value(transcriptRunnerChatRunContextKey{}).(*sessionRunnerChatRun)
	if run == nil || run.Transcript == nil {
		return nil
	}
	bound := run.Transcript
	if bound.Stream.Kind != transcriptstore.StreamKindFrameRef || bound.Stream.FrameID == "" || bound.Stream.OwnerID == "" {
		return nil
	}
	if err := s.validateLiveTranscriptRunnerClaim(ctx, bound.Claim); err != nil {
		return nil
	}
	stream, authoritative, err := s.resolveTranscriptFrameStream(ctx, run.SessionID)
	if err != nil || !authoritative || stream.UID != bound.Stream.UID || stream.OwnerID != bound.Stream.OwnerID || stream.FrameID != bound.Stream.FrameID {
		return nil
	}
	access, found, err := s.workspaceStore.GetKernelFrameAccessContext(ctx, stream.FrameID)
	if err != nil || !found || access.UserID != stream.OwnerID || access.Frame.RootFrameID != stream.RootFrameID {
		return nil
	}
	access, err = s.validateKernelHostIdentity(ctx, access)
	if err != nil {
		return nil
	}
	branch, err := s.transcriptStore.GetBranchState(ctx, stream.UID, stream.OwnerID)
	if err != nil || branch.ActiveBranchID == "" {
		return nil
	}
	return &backgroundShellOwner{UserID: access.UserID, ProjectID: access.Frame.ProjectID, FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID, RootFrameID: access.Frame.RootFrameID, RootFrameIncarnationID: access.RootFrameIncarnationID, BranchID: branch.ActiveBranchID}
}

// Called only after the native TaskOutput's positive blocking timer expires.
// The registry lifetime includes execution and terminal persistence; either is
// legitimate pending work. An arbitrary/stale tasks.json status grants nothing.
func (s *Server) observeTaskOutputPendingWait(ctx context.Context, taskID string) {
	observation, _ := ctx.Value(taskOutputWaitObservationKey{}).(*taskOutputWaitObservation)
	if observation == nil || ctx.Err() != nil {
		return
	}
	owner := s.currentBackgroundShellOwner(ctx)
	if owner == nil {
		return
	}
	task, found, err := s.taskStore.Get(taskID)
	if err != nil || !found || !taskOutputStillRunning(task.Status) {
		return
	}
	s.backgroundShellMu.Lock()
	defer s.backgroundShellMu.Unlock()
	entry := s.backgroundShells[taskID]
	observation.confirmed = entry != nil && entry.RunningCommand != nil && entry.owner != nil && *entry.owner == *owner
}
