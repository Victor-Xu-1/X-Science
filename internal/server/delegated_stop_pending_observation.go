package server

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

// Only deferred observation data lives here: no executable registration,
// cancellation handle or scheduling authority. Until Store acknowledges it,
// this proof is process-local and must never be advertised as restart-durable.
type delegatedStopPendingProof struct {
	key      delegatedStopCompletionKey
	observed workspace.DelegatedStopReceipt
	mu       sync.Mutex
	writing  bool
	resolved bool
	done     chan struct{}
	err      error
}

func (p *delegatedStopPendingProof) begin() (bool, <-chan struct{}, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resolved {
		return false, nil, p.err
	}
	if p.writing {
		return false, p.done, nil
	}
	p.writing = true
	p.done = make(chan struct{})
	return true, p.done, nil
}

func (p *delegatedStopPendingProof) result() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (s *Server) retainDelegatedStopProof(key delegatedStopCompletionKey, observed workspace.DelegatedStopReceipt) *delegatedStopPendingProof {
	s.delegatedStopProofsMu.Lock()
	defer s.delegatedStopProofsMu.Unlock()
	if s.delegatedStopProofs == nil {
		s.delegatedStopProofs = make(map[delegatedStopCompletionKey]*delegatedStopPendingProof)
	}
	if proof := s.delegatedStopProofs[key]; proof != nil {
		return proof
	}
	proof := &delegatedStopPendingProof{key: key, observed: freezeDelegatedStopReceipt(observed)}
	s.delegatedStopProofs[key] = proof
	return proof
}

// These canonical domain errors reject the original observation authority.
// A deleted scope retires its metadata; it is not a successful Stop ACK.
// SQL failures and context deadlines retain the proof for a later attempt.
func delegatedStopProofRetired(err error) bool {
	return errors.Is(err, workspace.ErrDelegatedStopScopeStale) || errors.Is(err, workspace.ErrDelegatedStopRequestConflict) ||
		errors.Is(err, workspace.ErrDelegatedStopUnavailable)
}

func (s *Server) completeDelegatedStopProof(proof *delegatedStopPendingProof, err error) {
	resolved := err == nil || delegatedStopProofRetired(err)
	proof.mu.Lock()
	proof.writing, proof.resolved, proof.err = false, resolved, err
	if resolved {
		s.delegatedStopProofsMu.Lock()
		if s.delegatedStopProofs[proof.key] == proof {
			delete(s.delegatedStopProofs, proof.key)
		}
		s.delegatedStopProofsMu.Unlock()
	}
	close(proof.done)
	proof.mu.Unlock()
	if delegatedStopProofRetired(err) {
		log.Printf("delegated Stop observation retired after canonical scope/request rejection: %v", err)
	} else if err != nil {
		log.Printf("delegated Stop observation persistence failed: %v", err)
	}
}

// Called only under an admitted finite operation or the original synchronous
// idle-drain evaluation fence. Store remains the sole persistence authority.
func (s *Server) persistDelegatedStopProof(ctx context.Context, proof *delegatedStopPendingProof) {
	_, err := s.workspaceStore.RecordDelegatedStopObservation(ctx, proof.key.owner, proof.observed)
	s.completeDelegatedStopProof(proof, err)
}

func (s *Server) enqueueDelegatedStopProof(key delegatedStopCompletionKey, observed workspace.DelegatedStopReceipt) {
	proof := s.retainDelegatedStopProof(key, observed)
	started, _, _ := proof.begin()
	if !started {
		return
	}
	// Actual completion triggers one finite attempt. No SQL runs on the signal
	// defer or registration cleanup, and failure never starts a retry loop.
	finish, err := s.beginAcceptedStopContinuation()
	if err != nil {
		s.completeDelegatedStopProof(proof, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer cancel()
		defer finish()
		s.persistDelegatedStopProof(ctx, proof)
	}()
}

func (s *Server) pendingDelegatedStopProofs(owner string, receipt workspace.DelegatedStopReceipt) []*delegatedStopPendingProof {
	s.delegatedStopProofsMu.Lock()
	defer s.delegatedStopProofsMu.Unlock()
	var pending []*delegatedStopPendingProof
	for key, proof := range s.delegatedStopProofs {
		if key.owner != owner || key.requestID != receipt.Request.RequestID || key.scope != receipt.Request.Scope {
			continue
		}
		for _, outcome := range receipt.Targets {
			if outcome.Target == key.target {
				pending = append(pending, proof)
				break
			}
		}
	}
	return pending
}

func (s *Server) allPendingDelegatedStopProofs() []*delegatedStopPendingProof {
	s.delegatedStopProofsMu.Lock()
	defer s.delegatedStopProofsMu.Unlock()
	pending := make([]*delegatedStopPendingProof, 0, len(s.delegatedStopProofs))
	for _, proof := range s.delegatedStopProofs {
		pending = append(pending, proof)
	}
	return pending
}

func (s *Server) hasPendingDelegatedStopProofs() bool {
	s.delegatedStopProofsMu.Lock()
	defer s.delegatedStopProofsMu.Unlock()
	return len(s.delegatedStopProofs) != 0
}
