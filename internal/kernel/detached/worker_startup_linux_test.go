//go:build linux

package detached

import (
	"strings"
	"testing"

	kernelruntime "x-science/internal/kernel"
	"x-science/internal/kernelcontract/bashsource"
)

func TestWorkerStartupAuthoritySurvivesDetachedSpecRoundTrip(t *testing.T) {
	spec := kernelruntime.SessionSpec{WorkerStartupPolicy: bashsource.CanonicalBashStdlibV1,
		SupervisorRuntimeGeneration: strings.Repeat("a", 64), KernelKind: "bash", Language: "python"}
	decoded := kernelSessionSpec(durableSessionSpec(spec))
	if decoded.WorkerStartupPolicy != spec.WorkerStartupPolicy || decoded.SupervisorRuntimeGeneration != spec.SupervisorRuntimeGeneration {
		t.Fatal("detached conversion dropped startup authority")
	}
}
