package server

import (
	"context"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

// Explicit readback/replay may retry already-completed proof under one finite
// caller budget. It never looks up, signals or executes a current registration.
func (s *Server) flushDelegatedStopProofs(ctx context.Context, owner string, receipt workspace.DelegatedStopReceipt) (bool, error) {
	pending := s.pendingDelegatedStopProofs(owner, receipt)
	if len(pending) == 0 {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, proof := range pending {
		if err := ctx.Err(); err != nil {
			return true, err
		}
		started, done, err := proof.begin()
		if err != nil {
			return true, err
		}
		if started {
			finish, err := s.beginRuntimeOperation()
			if err != nil {
				s.completeDelegatedStopProof(proof, err)
			} else {
				func() { defer finish(); s.persistDelegatedStopProof(ctx, proof) }()
			}
		} else if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				return true, ctx.Err()
			}
		}
		if err := proof.result(); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (s *Server) readDelegatedStopReceipt(ctx context.Context, owner, parent, requestID string) (workspace.DelegatedStopReceipt, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	receipt, found, err := s.workspaceStore.ReadDelegatedStop(ctx, owner, parent, requestID)
	if err != nil || !found {
		return receipt, found, err
	}
	flushed, err := s.flushDelegatedStopProofs(ctx, owner, receipt)
	if err != nil || !flushed {
		return receipt, found, err
	}
	return s.workspaceStore.ReadDelegatedStop(ctx, owner, parent, requestID)
}

// TryDrainIdle already holds its original evaluation fence. This synchronous
// finite flush is observation work, not an indefinite active scientific owner.
func (s *Server) flushDelegatedStopProofsForIdle(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, proof := range s.allPendingDelegatedStopProofs() {
		if err := ctx.Err(); err != nil {
			return err
		}
		started, done, err := proof.begin()
		if err != nil {
			if delegatedStopProofRetired(err) {
				continue
			}
			return err
		}
		if started {
			s.persistDelegatedStopProof(ctx, proof)
		} else if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := proof.result(); err != nil && !delegatedStopProofRetired(err) {
			return err
		}
	}
	return nil
}
