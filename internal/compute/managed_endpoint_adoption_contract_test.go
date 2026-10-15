package compute

import (
	"strings"
	"testing"
)

func TestManagedEndpointOwnerValidationRejectsNativeAdoption(t *testing.T) {
	for _, kind := range []string{"native_adopt", "native_fresh", "docker_adopt"} {
		t.Run(kind, func(t *testing.T) {
			spec := &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}
			switch kind {
			case "native_adopt":
				spec.Adopt = &ManagedEndpointRuntimeIdentity{Kind: "native-unit", Unit: "existing-owner.service", BootID: "fixture-boot",
					InvocationID: strings.Repeat("a", 32), ControlGroup: "/existing-owner", PID: 42, StartTicks: 12345}
			case "docker_adopt":
				spec.Kind = "docker"
				spec.Docker = &ManagedEndpointDockerSpec{Context: "default", Container: "existing-owner"}
				spec.Adopt = &ManagedEndpointRuntimeIdentity{Kind: "docker", DaemonID: "fixture-daemon", ContainerID: strings.Repeat("a", 64)}
			}
			err := spec.Validate()
			if kind == "native_adopt" {
				if err == nil {
					t.Fatal("unsupported native adoption was accepted for registration")
				}
			} else if err != nil {
				t.Fatalf("supported owner contract was rejected: %v", err)
			}
		})
	}
}
