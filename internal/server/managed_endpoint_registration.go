package server

import (
	"strings"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

// LegacyApproval is server-bound compatibility evidence, not a new approval
// decision. Restore an omitted field only when the complete candidate still
// equals the previously approved registration. Explicit input is never erased.
func restoreManagedEndpointLegacyApproval(registration compute.ManagedEndpointRegistration, existing workspace.ManagedEndpoint) (compute.ManagedEndpointRegistration, error) {
	if registration.Owner == nil || registration.Owner.Adopt == nil || registration.Owner.LegacyApproval != nil {
		return registration, nil
	}
	owner := *registration.Owner
	if existing.OwnerSpec == nil {
		owner.LegacyApproval = &compute.ManagedEndpointLegacyApproval{Registration: managedEndpointRegistration(existing), Hash: existing.ApprovedScriptHash}
		registration.Owner = &owner
		return registration, nil
	}
	if existing.OwnerSpec.LegacyApproval == nil {
		return registration, nil
	}
	stored := managedEndpointRegistration(existing)
	legacy := existing.OwnerSpec.LegacyApproval
	original := stored
	original.Owner = nil
	if existing.OwnerSpec.Validate() != nil || compute.ApprovedManagedEndpointHash(stored) != strings.TrimPrefix(existing.ApprovedScriptHash, "sha256:") ||
		legacy.Registration != original || compute.ApprovedManagedEndpointHash(original) != strings.TrimPrefix(legacy.Hash, "sha256:") {
		return registration, compute.ErrManagedEndpointCorruptOwner
	}
	owner.LegacyApproval = legacy
	candidate := registration
	candidate.Owner = &owner
	if compute.ApprovedManagedEndpointHash(candidate) != strings.TrimPrefix(existing.ApprovedScriptHash, "sha256:") {
		return registration, nil
	}
	return candidate, nil
}
