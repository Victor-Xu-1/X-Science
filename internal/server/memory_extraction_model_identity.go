package server

import (
	"context"
	"strings"
	"sync"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

type memoryExtractionCallIdentityKey struct{}

type memoryExtractionCallIdentity struct {
	mu       sync.Mutex
	identity workspace.MemoryExtractionModelIdentity
	request  *agentruntime.ModelRequest
}

func memoryExtractionIdentityFromResolved(resolved sessionRunnerResolvedModelClient) workspace.MemoryExtractionModelIdentity {
	identity := workspace.MemoryExtractionModelIdentity{
		Model: strings.TrimSpace(resolved.model), Selection: strings.TrimSpace(resolved.selection),
	}
	if resolved.identity != "" {
		identity.ProfileSHA256 = memoryViewDigest([]byte(resolved.identity))
	}
	if resolved.contextProfile != nil {
		identity.ProviderID = strings.TrimSpace(resolved.contextProfile.Provider.ID)
	}
	return identity
}

func recordMemoryExtractionCallIdentity(ctx context.Context, resolved sessionRunnerResolvedModelClient, request agentruntime.ModelRequest) {
	if ctx == nil {
		return
	}
	capture, _ := ctx.Value(memoryExtractionCallIdentityKey{}).(*memoryExtractionCallIdentity)
	if capture == nil {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.identity = memoryExtractionIdentityFromResolved(resolved)
	snapshot := agentruntime.SnapshotModelRequest(request)
	capture.request = &snapshot
}

func (capture *memoryExtractionCallIdentity) read() (*agentruntime.ModelRequest, workspace.MemoryExtractionModelIdentity) {
	if capture == nil {
		return nil, workspace.MemoryExtractionModelIdentity{}
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.request, capture.identity
}
