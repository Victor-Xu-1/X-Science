package workspace

import (
	"strings"
	"testing"

	"x-science/internal/kernelcontract/bashsource"
)

func TestKernelStartupEmptyFieldsPreserveCanonicalV1Bytes(t *testing.T) {
	input := KernelExecutionSessionSpecV1{Version: 1, KernelID: "k", OwnerUserID: "o", ProjectID: "p", RootFrameID: "r",
		RootFrameIncarnationID: "ri", FrameID: "f", FrameIncarnationID: "fi", AgentName: "a", KernelKind: "bash",
		Language: "python", Environment: "system", WorkspaceDir: "/tmp/startup"}
	_, raw, _, err := canonicalKernelExecutionSessionSpec(input)
	const legacy = `{"version":1,"kernel_id":"k","owner_user_id":"o","project_id":"p","root_frame_id":"r","root_frame_incarnation_id":"ri","frame_id":"f","frame_incarnation_id":"fi","agent_name":"a","kernel_kind":"bash","language":"python","environment":"system","workspace_dir":"/tmp/startup","mounts":[],"protected_paths":[],"fresh":false}`
	if err != nil || raw != legacy {
		t.Fatalf("legacy canonical bytes changed: %s %v", raw, err)
	}
	if _, err := DecodeKernelExecutionSessionSpecV1(legacy); err != nil {
		t.Fatal(err)
	}
	input.WorkerStartupPolicy = bashsource.CanonicalBashStdlibV1
	input.SupervisorRuntimeGeneration = strings.Repeat("a", 64)
	_, raw, _, err = canonicalKernelExecutionSessionSpec(input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeKernelExecutionSessionSpecV1(raw)
	if err != nil || decoded.WorkerStartupPolicy != input.WorkerStartupPolicy || decoded.SupervisorRuntimeGeneration != input.SupervisorRuntimeGeneration {
		t.Fatalf("profile roundtrip: %#v %v", decoded, err)
	}
	input.SupervisorRuntimeGeneration = ""
	if _, _, _, err := canonicalKernelExecutionSessionSpec(input); err == nil {
		t.Fatal("partial startup identity accepted")
	}
	input.WorkerStartupPolicy = "unknown"
	if _, _, _, err := canonicalKernelExecutionSessionSpec(input); err == nil {
		t.Fatal("unknown startup identity accepted")
	}
}
