package compute

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestManagedEndpointLegacyApprovalBytesRemainUnchanged(t *testing.T) {
	registration := ManagedEndpointRegistration{Name: "fixture", URL: "http://127.0.0.1:25000/v1", Port: 25000, CredentialName: "NVIDIA_API_KEY", SkillName: "using-model-endpoint", StartScript: "printf 'started\\n'", StopScript: "true", LivePath: "/health"}
	expected := `{"name":"fixture","url":"http://127.0.0.1:25000/v1","port":25000,"credentialName":"NVIDIA_API_KEY","skillName":"using-model-endpoint","startScript":"printf 'started\\n'","stopScript":"true","livePath":"/health"}`
	raw, err := json.Marshal(registration)
	if err != nil || string(raw) != expected {
		t.Fatalf("legacy bytes=%s err=%v", raw, err)
	}
	sum := sha256.Sum256([]byte(expected))
	approved := hex.EncodeToString(sum[:])
	if ApprovedManagedEndpointHash(registration) != approved {
		t.Fatal("legacy approval hash changed")
	}
	registration.StopScript = "false"
	if ApprovedManagedEndpointHash(registration) == approved {
		t.Fatal("script drift retained its approval")
	}
	registration.StopScript = "true"
	registration.Owner = &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}
	if ApprovedManagedEndpointHash(registration) == approved {
		t.Fatal("new owner authority was not included in approval")
	}
}

func TestManagedEndpointOwnerRejectsIncompleteAdoptionAndMixedProof(t *testing.T) {
	spec := &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}, Adopt: &ManagedEndpointRuntimeIdentity{Kind: "native-unit"}}
	if spec.Validate() == nil {
		t.Fatal("empty adoption unit was accepted")
	}
	spec.Adopt = &ManagedEndpointRuntimeIdentity{Kind: "native-unit", Unit: "-foreign.service", BootID: "boot", InvocationID: strings.Repeat("a", 32), ControlGroup: "/owner", PID: 1, StartTicks: 1}
	if spec.Validate() == nil {
		t.Fatal("option-like unit name was accepted")
	}
	var output cappedBuffer
	_, _ = output.Write([]byte(strings.Repeat("x", maxTranscriptBytes+1)))
	if output.Len() != maxTranscriptBytes {
		t.Fatal("bounded public transcript changed")
	}
}
