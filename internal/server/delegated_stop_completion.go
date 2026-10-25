package server

import (
	"sync"

	workspace "x-science/internal/persistence/workspace"
)

type delegatedStopCompletionKey struct {
	owner, requestID string
	scope            workspace.DelegatedStopScope
	target           workspace.DelegatedStopTarget
}

// Completion belongs to one registration, never the mutable frame-ID map.
// A replay retains only one callback for the same exact Stop request/target.
type delegatedStopRegistrationCompletion struct {
	mu        sync.Mutex
	finished  bool
	callbacks map[delegatedStopCompletionKey]func()
}

func (c *delegatedStopRegistrationCompletion) subscribe(key delegatedStopCompletionKey, callback func()) {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		callback()
		return
	}
	if c.callbacks == nil {
		c.callbacks = make(map[delegatedStopCompletionKey]func())
	}
	if _, exists := c.callbacks[key]; !exists {
		c.callbacks[key] = callback
	}
	c.mu.Unlock()
}

func (c *delegatedStopRegistrationCompletion) finish() {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	callbacks := c.callbacks
	c.callbacks = nil
	c.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

type delegatedStopRegistration struct {
	done       <-chan struct{}
	completion *delegatedStopRegistrationCompletion
}

type delegatedStopCompletion struct {
	server        *Server
	owner         string
	receipt       workspace.DelegatedStopReceipt
	index         int
	registrations []delegatedStopRegistration
	proofOnce     sync.Once
}

func freezeDelegatedStopReceipt(receipt workspace.DelegatedStopReceipt) workspace.DelegatedStopReceipt {
	receipt.Targets = append([]workspace.DelegatedStopOutcome(nil), receipt.Targets...)
	if receipt.Request.Target != nil {
		target := *receipt.Request.Target
		receipt.Request.Target = &target
	}
	return receipt
}

func (b *delegatedStopCompletion) subscribe() {
	key := delegatedStopCompletionKey{owner: b.owner, requestID: b.receipt.Request.RequestID,
		scope: b.receipt.Request.Scope, target: b.receipt.Targets[b.index].Target}
	for _, registration := range b.registrations {
		registration.completion.subscribe(key, b.enqueueIfDrained)
	}
	// An owner can finish between the exact signal and this deferred binding.
	// The complete captured set was sealed before any immediate replay runs.
	b.enqueueIfDrained()
}

func (b *delegatedStopCompletion) enqueueIfDrained() {
	for _, registration := range b.registrations {
		select {
		case <-registration.done:
		default:
			return
		}
	}
	b.proofOnce.Do(func() {
		observed := freezeDelegatedStopReceipt(b.receipt)
		observed.Targets[b.index].ExecutionObservation = "stopped"
		key := delegatedStopCompletionKey{owner: b.owner, requestID: observed.Request.RequestID,
			scope: observed.Request.Scope, target: observed.Targets[b.index].Target}
		b.server.enqueueDelegatedStopProof(key, observed)
	})
}
