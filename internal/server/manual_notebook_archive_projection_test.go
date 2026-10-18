package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func manualArchiveInventoryKernel(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	kernels, ok := response["kernels"].([]any)
	if !ok || len(kernels) != 1 {
		t.Fatalf("read-only inventory lost the actual local kernel: %#v", response)
	}
	kernel, ok := kernels[0].(map[string]any)
	if !ok || kernel["kernel_id"] != "manual-kernel" || kernel["frame_id"] != "manual-frame" {
		t.Fatalf("unexpected retained kernel: %#v", kernels[0])
	}
	return kernel
}

func TestManualNotebookArchiveInventoryRetainsReadsWithoutExecutableTarget(t *testing.T) {
	for _, state := range []string{"archived", "opaque-presentation", "unknown-archive-marker"} {
		t.Run(state, func(t *testing.T) {
			store, manager, app, access := newManualNotebookHTTPRuntime(t)
			frozen := manualPublicRequestBody(t, manager, access)
			path := "/api/frames/manual-frame/kernels"
			before := kernelAPIRequest(t, app, http.MethodGet, path, access.UserID, nil, http.StatusOK)
			kernel := manualArchiveInventoryKernel(t, before)
			if _, ok := kernel["manual_execution_target"].(map[string]any); !ok {
				t.Fatalf("fixture never advertised a real active local capability: %#v", kernel)
			}
			// Create a historical terminal receipt through the real durable
			// lifecycle. It never occupies the live Manager or pretends to run.
			record := admitManualPendingTestRecord(t, store, access, "manual-kernel",
				frozen["expected_kernel_generation"].(uint64), "print('historical receipt')")
			terminal, err := store.CompleteManualKernelExecution(context.Background(), workspace.ManualKernelCompletionInput{
				Expected: record, Disposition: "rejected_before_start", FinishedAt: time.Now().UTC(),
				Rejection: &workspace.ManualKernelRejection{Stderr: "Historical preflight rejection"},
			})
			if err != nil || terminal.State != "terminal" || terminal.StartedAt != nil || terminal.ExecutionLogID != "" {
				t.Fatalf("terminal fixture=%#v err=%v", terminal, err)
			}
			if state == "archived" {
				archived, err := store.UpdateConversationArchive(context.Background(), workspace.ConversationArchiveUpdate{
					OwnerID: access.UserID, FrameID: access.Frame.ID, IncarnationID: access.Frame.IncarnationID,
					ExpectedRevision: 0, Archived: true,
				})
				if err != nil || !archived.Changed {
					t.Fatalf("legal idle archive changed=%t err=%v", archived.Changed, err)
				}
			} else {
				var extra any = "opaque-legacy-presentation"
				if state == "unknown-archive-marker" {
					extra = map[string]any{"archived_at": "unknown"}
				}
				if _, err := store.SetFrameRuntimeMetadata(access.Frame.ID, workspace.FrameRuntimeMetadata{
					FrameID: access.Frame.ID, ContextData: map[string]any{"web_extra": extra, "runtime_owned": "retain"},
				}); err != nil {
					t.Fatal(err)
				}
			}
			_, metadata := archiveHTTPState(t, store, access.Frame.ID)
			archivedAt, archiveErr := transcriptstore.ConversationArchivedAt(metadata.ContextData)
			if state == "archived" {
				if archiveErr != nil || archivedAt <= 0 {
					t.Fatalf("archive command did not persist availability: at=%d err=%v", archivedAt, archiveErr)
				}
			} else if !errors.Is(archiveErr, transcriptstore.ErrConversationArchiveInvalid) {
				t.Fatalf("unknown archive fixture was not persisted: metadata=%#v err=%v", metadata, archiveErr)
			}
			for _, inventoryPath := range []string{path, "/api/kernels"} {
				response := kernelAPIRequest(t, app, http.MethodGet, inventoryPath, access.UserID, nil, http.StatusOK)
				retained := manualArchiveInventoryKernel(t, response)
				if target, present := retained["manual_execution_target"]; present {
					t.Errorf("%s %s advertised an executable target: %#v", state, inventoryPath, target)
				}
				if inventoryPath == path {
					scope, ok := response["manual_execution_scope"].(map[string]any)
					if !ok || len(scope) != 5 || scope["schema"] != "x-science.manual-kernel-scope.v1" ||
						scope["frame_id"] != access.Frame.ID || scope["root_frame_id"] != access.Frame.RootFrameID ||
						scope["frame_incarnation_id"] != access.Frame.IncarnationID || scope["root_frame_incarnation_id"] != access.RootFrameIncarnationID {
						t.Errorf("read-only recovery scope was lost or changed: %#v", response)
					}
				}
			}
			body := manualPendingRequestBody(terminal)
			response := kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(body), access.UserID, nil, http.StatusOK)
			readback, physical := manualPublicReceipt(t, response, body)
			if !reflect.DeepEqual(terminal, readback) || physical["status"] != "unavailable" {
				t.Errorf("read-only terminal receipt changed: before=%#v after=%#v physical=%#v", terminal, readback, physical)
			}
			page := readManualPendingTestPage(t, app, access, "")
			if len(page.pending) != 0 || !page.complete {
				t.Errorf("terminal history became new pending execution: %#v", page.raw)
			}
			_, after := archiveHTTPState(t, store, access.Frame.ID)
			if !reflect.DeepEqual(metadata, after) || manager.ActiveExecutionCount() != 0 || manager.ActiveCount() != 1 {
				t.Errorf("read-only projection mutated availability or runtime: before=%#v after=%#v executions=%d kernels=%d",
					metadata, after, manager.ActiveExecutionCount(), manager.ActiveCount())
			}
		})
	}
}

func TestManualNotebookArchiveAvailabilityErrorsDoNotBecomeTransient503(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{"archived", transcriptstore.ErrConversationArchived, http.StatusGone},
		{"unknown-archive-state", transcriptstore.ErrConversationArchiveInvalid, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeManualNotebookAPIError(recorder, fmt.Errorf("manual admission: %w", test.err))
			if recorder.Code != test.status {
				t.Errorf("availability error became retryable storage failure: status=%d want=%d body=%s",
					recorder.Code, test.status, recorder.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			code, ok := payload["code"].(string)
			if !ok || code == "" || code == "MANUAL_EXECUTION_UNAVAILABLE" || recorder.Header().Get(webErrorCodeHeader) != code {
				t.Errorf("availability error lacks a distinct, consistent code: %#v headers=%#v", payload, recorder.Header())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Error("manual availability error lost its no-store contract")
			}
		})
	}
}
