package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualNotebookPendingDiscoveryContinuesEmptyWindowAndRecoversOldTarget(t *testing.T) {
	store, manager, app, access := newManualPendingTestRuntime(t)
	old := admitManualPendingTestRecord(t, store, access, "old-manual-kernel", 7, "OLD_PRIVATE_SOURCE_MUST_NOT_LEAK")
	appendManualPendingOrdinaryEvents(t, store, access.Frame.ID, 128)
	first := readManualPendingTestPage(t, app, access, "")
	if len(first.pending) != 0 || first.complete || first.next == "" {
		t.Fatalf("empty first window discarded older pending: %#v", first.raw)
	}
	second := readManualPendingTestPage(t, app, access, first.next)
	if !second.complete || second.next != "" || second.through != first.through || len(second.pending) != 1 {
		t.Fatalf("older pending page=%#v", second.raw)
	}
	assertManualPendingSummary(t, second.pending[0], old)
	for _, page := range []manualPendingTestPage{first, second} {
		raw, err := json.Marshal(page.raw)
		if err != nil || strings.Contains(string(raw), old.Request.Source) || strings.Contains(string(raw), "ORDINARY_SOURCE_MUST_NOT_LEAK") || strings.Contains(string(raw), `"code"`) || strings.Contains(string(raw), `"source"`) {
			t.Fatalf("discovery leaked source: %s err=%v", raw, err)
		}
	}
	request := manualPendingRequestBody(old)
	response := kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(request), access.UserID, nil, http.StatusOK)
	durable, physical := manualPublicReceipt(t, response, request)
	if durable.ExecutionID != old.ExecutionID || physical["status"] != "unavailable" {
		t.Fatalf("historical target became live: %#v", response)
	}
	inventory := kernelAPIRequest(t, app, http.MethodGet, "/api/frames/manual-frame/kernels", access.UserID, nil, http.StatusOK)
	if kernels, ok := inventory["kernels"].([]any); !ok || len(kernels) != 0 || manager.ActiveExecutionCount() != 0 {
		t.Fatalf("discovery created current capability or execution: %#v", inventory)
	}
	if records, err := store.ListExecutionLog(access.Frame.ID, ""); err != nil || len(records) != 0 {
		t.Fatalf("discovery invented result=%#v err=%v", records, err)
	}
	again, found, err := store.GetManualKernelExecution(context.Background(), access, old.ExecutionID)
	if err != nil || !found || again.StateVersion != old.StateVersion || again.State != old.State {
		t.Fatalf("discovery modified lifecycle=%#v err=%v", again, err)
	}
}

func TestManualNotebookPendingDiscoveryOutputLimitDoesNotSkipReceipts(t *testing.T) {
	store, manager, app, access := newManualPendingTestRuntime(t)
	expected := make(map[string]bool)
	for index := 0; index < 35; index++ {
		record := admitManualPendingTestRecord(t, store, access, "historical-kernel", uint64(index+1), "PRIVATE_GENERATION_SOURCE")
		expected[record.ExecutionID] = false
	}
	first := readManualPendingTestPage(t, app, access, "")
	if len(first.pending) != 32 || first.complete {
		t.Fatalf("output bound page=%#v", first.raw)
	}
	second := readManualPendingTestPage(t, app, access, first.next)
	if len(second.pending) != 3 || !second.complete || second.through != first.through {
		t.Fatalf("continuation skipped or repeated candidates: %#v", second.raw)
	}
	for _, page := range []manualPendingTestPage{first, second} {
		for _, raw := range page.pending {
			summary := raw.(map[string]any)
			id, _ := summary["execution_id"].(string)
			seen, found := expected[id]
			if !found || seen {
				t.Fatalf("duplicate or unrelated pending receipt=%#v", summary)
			}
			expected[id] = true
		}
	}
	for id, seen := range expected {
		if !seen {
			t.Fatalf("lost pending receipt %s", id)
		}
	}
	if manager.ActiveExecutionCount() != 0 {
		t.Fatal("paged discovery dispatched code")
	}
}

func TestManualNotebookPendingReceiptKindPreservesTypedAndLegacyBodies(t *testing.T) {
	store, manager, app, access := newManualPendingTestRuntime(t)
	typed := admitManualPendingTestRecord(t, store, access, "typed-kernel", 7, "TYPED_SOURCE_PRIVATE")
	legacy := admitManualPendingTestRequest(t, store, access, "legacy-kernel", 3, workspace.ManualKernelRequest{
		RequestID: uuid.NewString(), Source: "LEGACY_SOURCE_PRIVATE", Language: "python", Environment: "python", TimeoutMillis: 5000,
	})
	t.Run("typed read preserves full request", func(t *testing.T) {
		request := manualPendingRequestBody(typed)
		response := kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(request), access.UserID, nil, http.StatusOK)
		if response["request_kind"] != "typed-v1" {
			t.Fatalf("typed receipt kind=%#v", response)
		}
		manualPublicReceipt(t, response, request)
	})
	t.Run("legacy read cannot manufacture typed retry authority", func(t *testing.T) {
		frozenTarget := manualPendingRequestBody(legacy)
		response := kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(frozenTarget), access.UserID, nil, http.StatusOK)
		if response["schema"] != "x-science.manual-kernel-receipt.v1" || response["request_kind"] != "legacy" {
			t.Fatalf("legacy union=%#v", response)
		}
		if _, present := response["request"]; present {
			t.Fatal("legacy receipt manufactured a typed request field")
		}
		raw, err := json.Marshal(response["durable"])
		if err != nil {
			t.Fatal(err)
		}
		var actual workspace.ManualKernelExecution
		if err := json.Unmarshal(raw, &actual); err != nil || !reflect.DeepEqual(actual, legacy) {
			t.Fatalf("legacy receipt altered original request/hash=%#v err=%v", actual, err)
		}
		physical, ok := response["physical"].(map[string]any)
		if !ok || physical["status"] != "unavailable" {
			t.Fatalf("legacy old boot became current execution=%#v", physical)
		}
		kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, access.UserID, frozenTarget, http.StatusConflict)
		kernelAPIRequest(t, app, http.MethodPost, manualPublicCancelURL(frozenTarget), access.UserID, frozenTarget, http.StatusConflict)
		wrong := manualPublicCopyBody(frozenTarget)
		wrong["expected_kernel_generation"] = legacy.KernelGeneration + 1
		kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(wrong), access.UserID, nil, http.StatusConflict)
		kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(frozenTarget), "foreign-owner", nil, http.StatusNotFound)
	})
	t.Run("discovery includes legacy without source", func(t *testing.T) {
		page := readManualPendingTestPage(t, app, access, "")
		if !page.complete || len(page.pending) != 2 {
			t.Fatalf("legacy missing from discovery=%#v", page.raw)
		}
		assertManualPendingSummary(t, page.pending[0], legacy)
		assertManualPendingSummary(t, page.pending[1], typed)
		raw, _ := json.Marshal(page.raw)
		if strings.Contains(string(raw), legacy.Request.Source) || strings.Contains(string(raw), typed.Request.Source) {
			t.Fatalf("discovery exposed code=%s", raw)
		}
	})
	for _, before := range []workspace.ManualKernelExecution{typed, legacy} {
		after, found, err := store.GetManualKernelExecution(context.Background(), access, before.ExecutionID)
		if err != nil || !found || !reflect.DeepEqual(before, after) {
			t.Fatalf("readonly recovery mutated canonical receipt=%#v err=%v", after, err)
		}
	}
	if manager.ActiveExecutionCount() != 0 {
		t.Fatal("readonly legacy recovery submitted code")
	}
}

func TestManualNotebookPendingScopeBootstrapDoesNotRequireLiveKernel(t *testing.T) {
	store, manager, app, root := newManualPendingTestRuntime(t)
	createKernelAPIProjectAndFrame(t, store, root.Frame.ProjectID, root.UserID, "pending-child", "OPERON", root.Frame.ID)
	child, found, err := store.GetKernelFrameAccess("pending-child")
	if err != nil || !found {
		t.Fatalf("child found=%t err=%v", found, err)
	}
	for _, access := range []workspace.KernelFrameAccess{root, child} {
		t.Run(access.Frame.ID, func(t *testing.T) {
			path := "/api/frames/" + access.Frame.ID + "/kernels"
			response := kernelAPIRequest(t, app, http.MethodGet, path, access.UserID, nil, http.StatusOK)
			scope, ok := response["manual_execution_scope"].(map[string]any)
			if !ok || len(scope) != 5 || scope["schema"] != "x-science.manual-kernel-scope.v1" || scope["frame_id"] != access.Frame.ID || scope["root_frame_id"] != root.Frame.ID ||
				scope["frame_incarnation_id"] != access.Frame.IncarnationID || scope["root_frame_incarnation_id"] != access.RootFrameIncarnationID {
				t.Fatalf("wrong empty inventory scope=%#v", response)
			}
			if kernels, ok := response["kernels"].([]any); !ok || len(kernels) != 0 {
				t.Fatalf("scope query fabricated a kernel=%#v", response)
			}
			denied := kernelAPIRequest(t, app, http.MethodGet, path, "foreign-owner", nil, http.StatusNotFound)
			if denied["manual_execution_scope"] != nil {
				t.Fatal("foreign response exposed scope")
			}
		})
	}
	missing := kernelAPIRequest(t, app, http.MethodGet, "/api/frames/missing-frame/kernels", root.UserID, nil, http.StatusNotFound)
	if missing["manual_execution_scope"] != nil || manager.ActiveExecutionCount() != 0 {
		t.Fatal("scope bootstrap created state or exposed an inaccessible scope")
	}
	// Preserve the legacy inventory envelope, but never turn its historical
	// local-actor fallback into a typed manual execution scope credential.
	createKernelAPIProjectAndFrame(t, store, "legacy-local-project", "local", "legacy-local-frame", "OPERON", "")
	legacy := kernelAPIRequest(t, app, http.MethodGet, "/api/frames/legacy-local-frame/kernels", "", nil, http.StatusOK)
	if _, present := legacy["manual_execution_scope"]; present {
		t.Fatal("legacy local fallback exposed a typed execution scope")
	}
}

func TestManualNotebookPendingScopeMatchesCurrentTargetWithoutSubstitutingGeneration(t *testing.T) {
	_, manager, app, access := newManualNotebookHTTPRuntime(t)
	frozen := manualPublicRequestBody(t, manager, access)
	response := kernelAPIRequest(t, app, http.MethodGet, "/api/frames/manual-frame/kernels", access.UserID, nil, http.StatusOK)
	scope, ok := response["manual_execution_scope"].(map[string]any)
	if !ok {
		t.Fatalf("live inventory lacks canonical scope=%#v", response)
	}
	target := response["kernels"].([]any)[0].(map[string]any)["manual_execution_target"].(map[string]any)
	for _, key := range []string{"frame_id", "root_frame_id", "frame_incarnation_id", "root_frame_incarnation_id"} {
		if scope[key] != target[key] {
			t.Fatalf("scope/target disagree at %s: scope=%#v target=%#v", key, scope, target)
		}
	}
	if _, exists := scope["kernel_generation"]; exists || target["kernel_generation"] != float64(frozen["expected_kernel_generation"].(uint64)) {
		t.Fatal("scope fabricated or replaced actual kernel generation")
	}
}
