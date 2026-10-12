package localcontainer

import (
	"errors"
	"strings"
)

// RuntimeIdentity is sourced from the canonical durable execution store. A
// historical name or matching labels alone cannot manufacture this receipt.
type RuntimeIdentity struct {
	ExecutionID           string
	ContainerID           string
	ImageID               string
	Environment           string
	EnvironmentGeneration string
}

func validateRuntimeIdentity(identity RuntimeIdentity, environment Environment, executionID string) error {
	if identity.ExecutionID != executionID || strings.TrimSpace(identity.ContainerID) == "" || len(identity.ContainerID) > 256 || strings.ContainsAny(identity.ContainerID, " /\\\x00\r\n\t") ||
		identity.ImageID != environment.ImageID || identity.Environment != environment.Name || identity.EnvironmentGeneration != environment.HostGeneration {
		return errors.New("persisted container identity conflicts with its admitted environment")
	}
	return nil
}

func validateContainerRuntimeReceipt(container dockerContainer, environment Environment, request ExecutionRequest) error {
	if request.Identity == nil {
		return validateExistingContainer(container, environment, request.ExecutionID)
	}
	if container.ID != request.Identity.ContainerID {
		return errors.Join(ErrExecutionOutcomeUnconfirmed, errors.New("actual container differs from the persisted runtime identity"))
	}
	if !request.BeforeNamespaceCutover {
		return validateExistingContainer(container, environment, request.ExecutionID)
	}
	labels := container.Config.Labels
	// These exact old labels are historical receipt inputs. They are never
	// emitted on a new container or accepted without a cutoff-bound actual ID.
	if container.Image != request.Identity.ImageID || labels["com.synonbiomed.managed"] != "true" ||
		labels["com.synonbiomed.execution"] != request.Identity.ExecutionID || labels["com.synonbiomed.environment"] != request.Identity.Environment ||
		labels["com.xscience.managed"] != "" || labels["com.xscience.execution"] != "" || labels["com.xscience.environment"] != "" {
		return errors.Join(ErrExecutionOutcomeUnconfirmed, errors.New("historical container metadata does not match its actual durable receipt"))
	}
	return nil
}
