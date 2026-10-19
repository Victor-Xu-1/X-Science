package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestSessionSpecialistKernelIdentityPreservesLegacyAndFrozenDefinitions(t *testing.T) {
	spec := SessionSpec{OwnerID: "owner", ProjectID: "project", FrameID: "frame", FrameIncarnationID: "frame-life", RootFrameID: "frame", RootFrameIncarnationID: "frame-life",
		AgentName: "synthetic-agent", KernelKind: "analysis", Language: "python", Environment: "python", WorkspaceDir: t.TempDir()}
	legacy, err := StableSessionID(spec)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{spec.OwnerID, spec.ProjectID, spec.RootFrameID, spec.RootFrameIncarnationID, spec.FrameID, spec.FrameIncarnationID,
		spec.AgentName, spec.DelegateName, spec.KernelKind, spec.Language, spec.Environment, spec.RuntimeGeneration, spec.WorkspaceDir}, "\x00")))
	if legacy != "kernel-"+hex.EncodeToString(digest[:12]) {
		t.Fatal("legacy stable identity changed")
	}
	spec.AgentDefinitionID = "immutable-definition-a"
	a, err := StableSessionID(spec)
	if err != nil {
		t.Fatal(err)
	}
	same, err := StableSessionID(spec)
	if err != nil || a != same {
		t.Fatal("same definition is not stable")
	}
	spec.AgentDefinitionID = "immutable-definition-b"
	b, err := StableSessionID(spec)
	if err != nil {
		t.Fatal(err)
	}
	if a == legacy || b == a || b == legacy {
		t.Fatal("different frozen definitions share one kernel")
	}
	if spec.RuntimeGeneration != "" {
		t.Fatal("Agent identity was hidden in the environment generation")
	}
}
