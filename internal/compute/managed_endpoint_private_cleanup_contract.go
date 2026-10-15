package compute

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

// ManagedEndpointPrivateCleanup captures deletion authority, not executable
// registration bytes, credentials, or a caller-selected runtime root.
type ManagedEndpointPrivateCleanup struct {
	Version         int                      `json:"version"`
	InstanceID      string                   `json:"instanceId"`
	UserID          string                   `json:"userId"`
	Endpoint        string                   `json:"endpoint"`
	ApprovedHash    string                   `json:"approvedHash"`
	StartGeneration int64                    `json:"startGeneration"`
	InitialAction   ManagedEndpointActionRef `json:"initialAction"`
	RuntimeRoot     string                   `json:"runtimeRoot"`
	ServiceDir      string                   `json:"serviceDir"`
	WorkspacePath   string                   `json:"workspacePath"`
}

func ManagedEndpointPrivateCleanupFor(instance ManagedEndpointOwnerInstance) (ManagedEndpointPrivateCleanup, error) {
	if err := instance.Validate(); err != nil {
		return ManagedEndpointPrivateCleanup{}, err
	}
	return ManagedEndpointPrivateCleanup{Version: 1, InstanceID: instance.ID, UserID: instance.UserID,
		Endpoint: instance.Endpoint, ApprovedHash: instance.ApprovedHash, StartGeneration: instance.StartGeneration,
		InitialAction: instance.InitialAction, RuntimeRoot: instance.RuntimeRoot,
		ServiceDir: instance.ServiceDir, WorkspacePath: instance.WorkspacePath}, nil
}

// Key also validates a decoded scope before it can select a direct child or
// recognize the one deterministic trash directory left by its prior attempt.
func (scope ManagedEndpointPrivateCleanup) Key() (string, error) {
	if scope.Version != 1 || !validManagedOwnerComponent(scope.InstanceID) || scope.UserID == "" || scope.Endpoint == "" ||
		scope.StartGeneration < 1 || !filepath.IsAbs(scope.RuntimeRoot) || !filepath.IsAbs(scope.ServiceDir) || !filepath.IsAbs(scope.WorkspacePath) ||
		scope.InitialAction.InstanceID != scope.InstanceID || scope.InitialAction.Generation != scope.StartGeneration {
		return "", errors.New("managed endpoint private cleanup scope is invalid")
	}
	if err := scope.InitialAction.Validate(); err != nil {
		return "", err
	}
	hash := strings.TrimPrefix(scope.ApprovedHash, "sha256:")
	if len(hash) != sha256.Size*2 {
		return "", errors.New("managed endpoint private cleanup approval hash is invalid")
	}
	if decoded, err := hex.DecodeString(hash); err != nil || len(decoded) != sha256.Size {
		return "", errors.New("managed endpoint private cleanup approval hash is invalid")
	}
	raw, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
