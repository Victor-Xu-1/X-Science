package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

func managedEndpointRegistration(endpoint workspace.ManagedEndpoint) compute.ManagedEndpointRegistration {
	credential := ""
	if endpoint.CredentialName != nil {
		credential = *endpoint.CredentialName
	}
	return compute.ManagedEndpointRegistration{Name: endpoint.Name, URL: endpoint.URL, Port: endpoint.Port, CredentialName: credential, SkillName: endpoint.SkillName, StartScript: endpoint.StartScript, StopScript: endpoint.StopScript, LivePath: endpoint.LivePath, Owner: endpoint.OwnerSpec}
}

func (s *Server) managedEndpointCoordinator() (*compute.ManagedEndpointCoordinator, error) {
	root := filepath.Join(s.fileRoot+"-managed-endpoints", ".owners")
	if s.workspaceStore == nil || !filepath.IsAbs(root) {
		return nil, errors.New("managed endpoint owner authority is unavailable")
	}
	runtime := s.managedEndpointRuntime
	if runtime == nil {
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		runtime, err = compute.NewManagedEndpointSystemRuntime(root, executable, s.workspaceStore)
		if err != nil {
			return nil, err
		}
	}
	return &compute.ManagedEndpointCoordinator{Store: s.workspaceStore, Runtime: runtime, RuntimeRoot: root, Environment: s.managedEndpointEnvironment, CancelAuthorized: func(ctx context.Context) bool { return errors.Is(context.Cause(ctx), ErrGenerationStopped) }}, nil
}

func (s *Server) managedEndpointEnvironment(ctx context.Context, instance compute.ManagedEndpointOwnerInstance, _ string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	values := map[string]string{"HOST_PORT": strconv.Itoa(instance.Registration.Port), "SERVICE_DIR": instance.ServiceDir}
	name := instance.Registration.CredentialName
	if name != "" {
		if name != "NVIDIA_API_KEY" {
			return nil, errors.New("managed endpoint credential name is not approved")
		}
		credential, err := s.kernelHostCredentialGet(instance.UserID, name)
		if err != nil {
			return nil, errors.New("approved managed endpoint credential is unavailable")
		}
		for _, key := range []string{"value", "token", "api_key", "key"} {
			if value := strings.TrimSpace(stringValue(mapValue(credential)[key])); value != "" {
				values[name] = value
				break
			}
		}
		if values[name] == "" {
			return nil, errors.New("approved managed endpoint credential has no scalar value")
		}
	}
	return values, nil
}

// An old registration is still readable with its original hash, but neither a
// port nor an arbitrary historical script is proof of a physical owner.
func (s *Server) claimOwnedManagedEndpoint(name, userID, kind string) (workspace.ManagedEndpoint, bool, error) {
	if _, err := s.ownedManagedEndpoint(name, userID); err != nil {
		return workspace.ManagedEndpoint{}, false, err
	}
	if kind == "start" {
		return s.workspaceStore.ClaimManagedEndpointStart(name, userID)
	}
	return s.workspaceStore.ClaimManagedEndpointStop(name, userID)
}

func (s *Server) ownedManagedEndpoint(name, userID string) (workspace.ManagedEndpoint, error) {
	values, err := s.workspaceStore.ListManagedEndpoints(userID, name, false)
	if err != nil {
		return workspace.ManagedEndpoint{}, err
	}
	if len(values) != 1 {
		return workspace.ManagedEndpoint{}, sql.ErrNoRows
	}
	if values[0].Location != "remote" && values[0].OwnerSpec == nil {
		return workspace.ManagedEndpoint{}, compute.ErrManagedEndpointOwnershipUnknown
	}
	if values[0].Location != "remote" && compute.ApprovedManagedEndpointHash(managedEndpointRegistration(values[0])) != strings.TrimPrefix(values[0].ApprovedScriptHash, "sha256:") {
		return workspace.ManagedEndpoint{}, errors.New("stored scripts no longer match the approved hash")
	}
	return values[0], nil
}

func (s *Server) managedEndpointCoordinatorFor(name, userID string) (*compute.ManagedEndpointCoordinator, error) {
	endpoint, err := s.ownedManagedEndpoint(name, userID)
	if err != nil {
		return nil, err
	}
	if endpoint.Location == "remote" {
		return nil, nil
	}
	return s.managedEndpointCoordinator()
}

func (s *Server) runOwnedManagedEndpoint(ctx context.Context, endpoint workspace.ManagedEndpoint, coordinator *compute.ManagedEndpointCoordinator) (string, error) {
	if endpoint.Location == "remote" {
		return s.workspaceStore.FinishManagedEndpointStop(endpoint.ClaimHandle, "", nil)
	}
	claim := compute.ManagedEndpointClaim{UserID: endpoint.RegisteredBy, Name: endpoint.Name, Handle: endpoint.ClaimHandle, Generation: endpoint.ClaimGeneration, Registration: managedEndpointRegistration(endpoint), ApprovedHash: endpoint.ApprovedScriptHash, ServiceDir: endpoint.ServiceDir}
	state, _, err := coordinator.Run(ctx, claim)
	return state, err
}

// This supervisor only observes stored action references. It never retries a
// start or creates a second compute scheduler on a web-process restart.
func (s *Server) RunManagedEndpointOwners(ctx context.Context) error {
	if s.workspaceStore == nil {
		return nil
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var cursor *compute.ManagedEndpointActionRef
	for {
		pending, err := s.workspaceStore.PendingManagedEndpointActions(ctx, cursor, 16)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			cursor = nil
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-ticker.C:
			}
			continue
		}
		coordinator, err := s.managedEndpointCoordinator()
		if err != nil {
			return err
		}
		cursor, err = coordinator.Reconcile(ctx, cursor, 16)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
		}
	}
}
