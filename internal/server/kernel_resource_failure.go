package server

import (
	"errors"
	kernelruntime "x-science/internal/kernel"
	"x-science/internal/processsupervisor"
)

func kernelResourcePressureReceipt(outcome kernelruntime.ExecutionOutcome) *processsupervisor.MemoryPressure {
	var failure *kernelruntime.ResourcePressureFailure
	if errors.As(outcome.Err, &failure) && processsupervisor.ValidHostMemoryPressure(failure.Observation) {
		return &failure.Observation
	}
	if outcome.HostResources != nil && outcome.HostResources.Validate() == nil {
		return outcome.HostResources.PressureFailure
	}
	return nil
}
