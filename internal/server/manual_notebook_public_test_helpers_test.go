package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/google/uuid"
	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

const manualPublicCollection = "/api/frames/manual-frame/kernel-executions"

// This test fixture freezes the actual Manager generation, without depending
// on the new public target projection that the RED tests are intended to add.
func manualPublicRequestBody(t *testing.T, manager *kernelruntime.Manager, access workspace.KernelFrameAccess) map[string]any {
	t.Helper()
	var generation uint64
	for _, candidate := range manager.SnapshotIdleCandidates() {
		if candidate.KernelID == "manual-kernel" && candidate.FrameID == access.Frame.ID {
			generation = candidate.Generation
		}
	}
	if generation == 0 {
		t.Fatal("fixture has no actual idle Manager generation")
	}
	return map[string]any{
		"schema": "x-science.manual-kernel-request.v1", "request_id": uuid.NewString(),
		"expected_frame_incarnation_id":      access.Frame.IncarnationID,
		"expected_root_frame_id":             access.Frame.RootFrameID,
		"expected_root_frame_incarnation_id": access.RootFrameIncarnationID,
		"kernel_id":                          "manual-kernel", "expected_kernel_generation": generation,
		"language": "python", "environment": "python", "code": "print('typed once')", "timeout_ms": int64(5000),
	}
}

func manualPublicLookupURL(body map[string]any) string {
	query := url.Values{}
	for _, key := range []string{
		"expected_frame_incarnation_id", "expected_root_frame_id", "expected_root_frame_incarnation_id",
		"kernel_id", "expected_kernel_generation", "language", "environment",
	} {
		query.Set(key, fmt.Sprint(body[key]))
	}
	return manualPublicCollection + "/by-request/" + url.PathEscape(body["request_id"].(string)) + "?" + query.Encode()
}

func manualPublicCancelURL(body map[string]any) string {
	return manualPublicCollection + "/by-request/" + url.PathEscape(body["request_id"].(string)) + "/cancel"
}

func manualPublicReceipt(t *testing.T, response, request map[string]any) (workspace.ManualKernelExecution, map[string]any) {
	t.Helper()
	if response["schema"] != "x-science.manual-kernel-receipt.v1" {
		t.Fatalf("receipt schema=%#v", response)
	}
	wantRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	gotRequest, err := json.Marshal(response["request"])
	if err != nil || string(gotRequest) != string(wantRequest) {
		t.Fatalf("original frozen request was not echoed: got=%s want=%s err=%v", gotRequest, wantRequest, err)
	}
	raw, err := json.Marshal(response["durable"])
	if err != nil {
		t.Fatal(err)
	}
	var durable workspace.ManualKernelExecution
	if err := json.Unmarshal(raw, &durable); err != nil || durable.ExecutionID == "" || durable.RequestSHA256 == "" || durable.Request.RequestID != request["request_id"] {
		t.Fatalf("typed durable receipt=%#v err=%v", durable, err)
	}
	physical, ok := response["physical"].(map[string]any)
	if !ok || physical["status"] == nil || physical["sampled_at"] == nil {
		t.Fatalf("missing separate physical observation: %#v", response)
	}
	return durable, physical
}

func manualPublicCopyBody(body map[string]any) map[string]any {
	copy := make(map[string]any, len(body))
	for key, value := range body {
		copy[key] = value
	}
	return copy
}
