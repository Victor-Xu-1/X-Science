package server

import (
	"context"
	"strings"

	kernelruntime "x-science/internal/kernel"
)

const localComputeProviderID = "local"

type computeSoftwareRuntimePolicy struct {
	Kind         string `json:"kind"`
	ProviderName string `json:"providerName,omitempty"`
}

// Only the software_runtime bridge is described here, not generic tool access.
// providerFound is an owner-scoped catalog fact, never a probe or credential grant.
func computeSoftwarePolicy(values []string, configured, providerFound bool) computeSoftwareRuntimePolicy {
	if len(values) == 0 {
		if configured {
			return computeSoftwareRuntimePolicy{Kind: "local_explicit"}
		}
		return computeSoftwareRuntimePolicy{Kind: "local_default"}
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return computeSoftwareRuntimePolicy{Kind: "invalid_selection"}
	}
	if !providerFound {
		return computeSoftwareRuntimePolicy{Kind: "unavailable"}
	}
	provider := strings.TrimSpace(values[0])
	if provider == localComputeProviderID {
		return computeSoftwareRuntimePolicy{Kind: "local_explicit"}
	}
	return computeSoftwareRuntimePolicy{Kind: "remote_unsupported", ProviderName: provider}
}

// The projection and existing resolver share one raw selection observation.
func (s *Server) readSessionComputeSelection(userID, rootFrameID string) ([]string, bool, computeSoftwareRuntimePolicy, error) {
	providers, configured, err := s.workspaceStore.SessionComputeProviderSelection(userID, rootFrameID)
	if err != nil {
		return nil, false, computeSoftwareRuntimePolicy{}, err
	}
	found := false
	if len(providers) == 1 && strings.TrimSpace(providers[0]) != "" {
		_, found, err = s.workspaceStore.GetComputeProvider(strings.TrimSpace(providers[0]), userID)
		if err != nil {
			return providers, configured, computeSoftwareRuntimePolicy{Kind: "unavailable"}, err
		}
	}
	return providers, configured, computeSoftwarePolicy(providers, configured, found), nil
}

// resolveSelectedComputeProvider is the sole authority bridge from the
// conversation selector to an execution provider. An untouched selector and
// an explicit local choice both mean the local machine. Explicit remote
// choices are preserved and can never be silently rerouted to local.
func (s *Server) resolveSelectedComputeProvider(ctx context.Context, userID, rootFrameID string) (string, error) {
	if s == nil || s.workspaceStore == nil {
		return "", kernelruntime.NewHostCallError("unavailable", "compute provider selection authority is unavailable")
	}
	providers, _, policy, err := s.readSessionComputeSelection(userID, rootFrameID)
	if err != nil {
		if policy.Kind == "unavailable" {
			return "", kernelruntime.NewHostCallError("unavailable", "selected compute provider is unavailable")
		}
		return "", kernelruntime.NewHostCallError("unavailable", "compute provider selection is unavailable")
	}
	if policy.Kind == "local_default" || policy.Kind == "local_explicit" {
		return localComputeProviderID, nil
	}
	if policy.Kind == "invalid_selection" {
		if len(providers) != 1 {
			return "", kernelruntime.NewHostCallError("invalid_arguments", "select exactly one compute provider before execution")
		}
		return "", kernelruntime.NewHostCallError("invalid_arguments", "compute provider selection is invalid")
	}
	if policy.Kind == "unavailable" {
		return "", kernelruntime.NewHostCallError("unavailable", "selected compute provider is unavailable")
	}
	return policy.ProviderName, nil
}
