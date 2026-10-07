package server

import (
	"context"
	"errors"
	"time"

	workspace "synon-go/internal/persistence/workspace"
)

func (s *Server) agentComputeHandleDeadline(ctx context.Context, access workspace.KernelFrameAccess, handleID, provider, sandboxID string) (time.Time, error) {
	entry, found, err := s.runtimeStore.Get(computeProviderHandleNamespace, handleID)
	if err != nil || !found {
		return time.Time{}, errors.New("compute handle lifetime receipt is unavailable")
	}
	value := mapValue(entry.Value)
	if stringValue(value["owner_user_id"]) != access.UserID || stringValue(value["root_frame_id"]) != access.Frame.RootFrameID || stringValue(value["sandbox_id"]) != sandboxID {
		return time.Time{}, errors.New("compute handle lifetime authority changed")
	}
	var deadline time.Time
	if epoch := int64(numberValue(value["sandbox_deadline_epoch"])); epoch > 0 {
		deadline = time.Unix(epoch, 0).UTC()
	}
	original, known, err := s.workspaceStore.ComputeInstanceDeadline(ctx, access.UserID, provider, sandboxID)
	if err != nil {
		return time.Time{}, err
	}
	if known && (deadline.IsZero() || original.Before(deadline)) {
		deadline = original
	}
	if deadline.IsZero() {
		return time.Time{}, errors.New("physical instance lifetime is unknown; reconcile or select an authorized long-lived resource before submitting more work")
	}
	return deadline, nil
}
