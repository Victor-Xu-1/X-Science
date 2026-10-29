package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"x-science/internal/kernelcontract/bashsource"
)

type startupWriteBuffer struct{ bytes.Buffer }

func (*startupWriteBuffer) Close() error { return nil }

func TestBashStartupIdentityPreservesLegacyAndSeparatesCapability(t *testing.T) {
	legacy := SessionSpec{OwnerID: "o", ProjectID: "p", RootFrameID: "r", RootFrameIncarnationID: "ri",
		FrameID: "f", FrameIncarnationID: "fi", AgentName: "a", KernelKind: "bash", Language: "python",
		Environment: "system", WorkspaceDir: "/tmp/startup"}
	id, err := StableSessionID(legacy)
	if err != nil {
		t.Fatal(err)
	}
	// Frozen before adding startup fields: the empty capability adds no bytes.
	const oldID = "kernel-14e7546709bac6df7b707128"
	if id != oldID {
		t.Fatalf("legacy identity changed: %s", id)
	}
	trusted := legacy
	trusted.WorkerStartupPolicy = bashsource.CanonicalBashStdlibV1
	trusted.SupervisorRuntimeGeneration = strings.Repeat("a", 64)
	newID, err := StableSessionID(trusted)
	if err != nil || id == newID || sameSessionSpec(legacy, trusted) || sessionRestartKey(legacy) == sessionRestartKey(trusted) {
		t.Fatalf("startup authority reused legacy identity: %s %s %v", id, newID, err)
	}
	trusted.WorkerStartupPolicy = "unknown"
	if _, err := StableSessionID(trusted); err == nil {
		t.Fatal("unknown capability accepted")
	}
}

func TestBashStartupAllHostWritersRefusePythonAndSDKBypass(t *testing.T) {
	buffer := &startupWriteBuffer{}
	w := &Worker{startupPolicy: bashsource.CanonicalBashStdlibV1, stdin: buffer, done: make(chan struct{})}
	for _, code := range []string{"print('arbitrary')", "import x_science_host_bridge\nx_science_host_bridge.bind_cell('x',False,globals(),None)"} {
		if _, err := w.Execute(context.Background(), code, "agent"); err == nil {
			t.Fatal("generic Execute bypassed profile")
		}
	}
	if w.observationTainted {
		t.Fatal("refused execution changed interpreter provenance")
	}
	code, _ := bashsource.BashPythonWrapper("printf canonical")
	frame, _ := json.Marshal(map[string]any{"id": "restricted", "code": code, "host_enabled": true})
	if err := w.writeProtocol(frame); err == nil || buffer.Len() != 0 {
		t.Fatal("host-enabled request reached stdin")
	}
	frame, _ = json.Marshal(map[string]any{"id": "restricted", "code": code, "host_enabled": false})
	if err := w.writeProtocol(frame); err != nil || buffer.Len() == 0 {
		t.Fatalf("canonical request failed: %v", err)
	}
}
