package server

import (
	"context"
	"path/filepath"
	"time"
)

// The publisher may replace the output directory with its receipt-bound
// compatibility link. Verification must share the SAME per-output lock so it
// never observes that legitimate transition as missing or foreign evidence.
func (s *Server) verifyAndPublishManagedExecutionOutputAuthority(
	ctx context.Context, workspaceRoot, outputRoot, packID, executionID string, writes map[string]string,
) (managedExecutionOutputAuthority, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	unlock, err := lockManagedExecutionSnapshot(ctx, outputRoot)
	if err != nil {
		return managedExecutionOutputAuthority{}, err
	}
	defer unlock()
	authority, err := s.verifyManagedExecutionOutputAuthorityLocked(ctx, workspaceRoot, outputRoot, packID, executionID, writes)
	if err != nil {
		return managedExecutionOutputAuthority{}, err
	}
	if err := publishManagedExecutionOutputSnapshotLocked(ctx, workspaceRoot, authority); err != nil {
		return managedExecutionOutputAuthority{}, err
	}
	authority.ResolvedRoot = managedExecutionSnapshotDirectory(workspaceRoot, authority)
	return authority, nil
}

func lockManagedExecutionSnapshot(ctx context.Context, outputRoot string) (func(), error) {
	lock := agentWorkspaceEditLock("managed-execution-snapshot:\x00" + filepath.Clean(outputRoot))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lock.TryLock() {
		return lock.Unlock, nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if lock.TryLock() {
				return lock.Unlock, nil
			}
		}
	}
}
