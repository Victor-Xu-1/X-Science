package server

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

// resolveUnprojectedApprovedFrame admits only a direct, explicitly approved
// capability on a durable Frame which has not acquired Transcript authority.
// It supplies identity, never a persisted path or process/confinement authority.
func (s *Server) resolveUnprojectedApprovedFrame(ctx context.Context, sessionID string) (workspace.KernelFrameAccess, bool) {
	if s == nil || s.workspaceStore == nil || ctx == nil || ctx.Err() != nil {
		return workspace.KernelFrameAccess{}, false
	}
	if ctx.Value(transcriptRunnerChatRunContextKey{}) != nil || ctx.Value(transcriptArtifactRunContextKey{}) != nil {
		return workspace.KernelFrameAccess{}, false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return workspace.KernelFrameAccess{}, false
	}
	access, found, err := s.workspaceStore.GetKernelFrameAccessContext(ctx, sessionID)
	if err != nil || !found || access.Frame.ID != sessionID {
		return workspace.KernelFrameAccess{}, false
	}
	access, err = s.validateKernelHostIdentity(ctx, access)
	if err != nil {
		return workspace.KernelFrameAccess{}, false
	}
	repo := s.transcriptStore
	if repo == nil {
		repo, err = s.workspaceStore.TranscriptRepository(ctx)
		if err != nil {
			return workspace.KernelFrameAccess{}, false
		}
	}
	if _, found, err := repo.GetFrameAuthorityBySession(ctx, access.UserID, sessionID); err != nil || found {
		return workspace.KernelFrameAccess{}, false
	}
	if _, found, err := repo.GetStreamBySession(ctx, access.UserID, sessionID); err != nil || found {
		return workspace.KernelFrameAccess{}, false
	}
	// Missing authority is not enough: an orphaned or foreign canonical stream
	// must not be mistaken for a Frame which has never been projected.
	if _, err := repo.GetStream(ctx, "frame:"+access.Frame.ID, access.UserID); !errors.Is(err, sql.ErrNoRows) {
		return workspace.KernelFrameAccess{}, false
	}
	return access, true
}
