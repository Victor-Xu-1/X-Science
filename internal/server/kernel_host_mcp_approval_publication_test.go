package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	workspace "synon-go/internal/persistence/workspace"
	"synon-go/internal/tools/mcpstdio"
)

func TestKernelMCPPublicApprovalLifecycle(t *testing.T) {
	for _, action := range []string{"allow_once", "deny", "cancel", "wrong_frame", "unsupported_scope"} {
		t.Run(action, func(t *testing.T) {
			store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
			defer closeKernelHostTestRuntime(t, app, manager, store)
			if _, err := app.settingsStore.Set(approvalDefaultsSettingKey, map[string]any{"mode": "ask"}); err != nil {
				t.Fatal(err)
			}
			resolution := kernelMCPResolution{
				connector: workspaceMCPRuntimeConnector{ID: "custom:remote", Name: "remote", Source: "custom"},
				tool:      mcpstdio.ToolProjection{Name: "mcp__remote__echo", ToolName: "echo", ReadOnlyHint: true},
			}
			input := map[string]any{"text": "reviewed input"}
			permission := app.kernelMCPPermissionResult(context.Background(), identity.access.Frame.ID, resolution,
				agentruntime.ToolCall{ID: "visible-host-call", Name: resolution.tool.Name}, input)
			approvalID := stringValue(permission["approvalId"])
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			returned := make(chan error, 1)
			go func() {
				returned <- app.requireVisibleKernelMCPApproval(ctx, identity.access, resolution, input, permission)
			}()
			pending := waitKernelMCPPublicInput(t, store, identity.access.Frame.ID, approvalID)
			if pending["kind"] != agentToolApprovalKind || pending["mode"] != "ro" || pending["rememberable"] != false {
				t.Fatalf("incorrect MCP approval presentation: %#v", pending)
			}
			frame, _, err := store.GetCompatibilityFrame(identity.access.Frame.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := "approved"
			wantError := false
			switch action {
			case "cancel":
				cancel()
				wantStatus, wantError = "failed", true
			case "wrong_frame", "unsupported_scope":
				response := compatibilityInputResponse{RequestID: approvalID, Action: "allow_once", Scope: "once"}
				if action == "wrong_frame" {
					frame.ID = "foreign-frame"
				} else {
					response.Action, response.Scope = "allow_always", "always"
				}
				_, handled, err := app.resolveKernelMCPApprovalInputs(context.Background(), frame,
					map[string]map[string]any{approvalID: pending}, []compatibilityInputResponse{response})
				if !handled || err == nil {
					t.Fatalf("invalid authority/scope was accepted: handled=%t err=%v", handled, err)
				}
				entry, _, _ := app.runtimeStore.Get(agentRuntimeApprovalNamespace, approvalID)
				if mapValue(entry.Value)["status"] != "pending" {
					t.Fatal("invalid approval changed the decision")
				}
				cancel()
				wantStatus, wantError = "failed", true
			default:
				approved := action == "allow_once"
				response := compatibilityInputResponse{RequestID: approvalID, Action: action, Approved: &approved}
				if approved {
					// Match the real browser: allow with omitted once scope and
					// no tool_id when the request only has its approval requestId.
					response.Action, response.Mode = "allow", "ro"
				}
				result, err := app.resolveCompatibilityInput(httptest.NewRequest(http.MethodPost, "/resolve-input", nil), frame,
					compatibilityResolveInputRequest{Responses: []compatibilityInputResponse{response}})
				if err != nil || len(result.RemainingIDs) != 0 {
					t.Fatalf("MCP public response=%#v err=%v", result, err)
				}
				if !approved {
					wantStatus, wantError = "denied", true
				}
			}
			select {
			case err := <-returned:
				if (err != nil) != wantError {
					t.Fatalf("MCP wait=%v wantError=%t", err, wantError)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("MCP callback did not settle")
			}
			entry, _, err := app.runtimeStore.Get(agentRuntimeApprovalNamespace, approvalID)
			if err != nil || mapValue(entry.Value)["status"] != wantStatus {
				t.Fatalf("MCP decision=%#v want=%s err=%v", entry, wantStatus, err)
			}
			metadata, _, err := store.GetFrameRuntimeMetadata(identity.access.Frame.ID)
			if err != nil || len(compatibilityServerPendingInputs(metadata.ContextData)) != 0 {
				t.Fatalf("MCP approval left a stale public card: %#v err=%v", metadata.ContextData, err)
			}
		})
	}
}

func waitKernelMCPPublicInput(t *testing.T, store *workspace.Store, frameID, approvalID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		metadata, _, err := store.GetFrameRuntimeMetadata(frameID)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range compatibilityServerPendingInputs(metadata.ContextData) {
			if compatibilityServerPendingInputID(item) == approvalID {
				return item
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nested MCP approval was not published")
	return nil
}

func TestKernelMCPExpiredApprovalRetirementRequiresOwnedTerminalAudit(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	if _, err := app.settingsStore.Set(approvalDefaultsSettingKey, map[string]any{"mode": "ask"}); err != nil {
		t.Fatal(err)
	}
	resolution := kernelMCPResolution{
		connector: workspaceMCPRuntimeConnector{ID: "custom:remote", Name: "remote", Source: "custom"},
		tool:      mcpstdio.ToolProjection{Name: "mcp__remote__echo", ToolName: "echo", ReadOnlyHint: true},
	}
	ids := make(map[string]string)
	for _, name := range []string{"cancelled", "active", "approved"} {
		input := map[string]any{"text": name}
		permission := app.kernelMCPPermissionResult(context.Background(), identity.access.Frame.ID, resolution,
			agentruntime.ToolCall{ID: name, Name: resolution.tool.Name}, input)
		ids[name] = stringValue(permission["approvalId"])
		audit := workspace.KernelMCPAuditInput{CallID: name, FrameID: identity.access.Frame.ID,
			RootFrameID: identity.access.Frame.RootFrameID, OwnerUserID: identity.access.UserID,
			Server: "remote", Method: "echo", Input: input}
		if _, err := store.BeginKernelMCPAudit(context.Background(), audit); err != nil {
			t.Fatal(err)
		}
		if name != "active" {
			if _, err := store.FinishKernelMCPAudit(context.Background(), workspace.KernelMCPAuditTerminalInput{
				KernelMCPAuditInput: audit, Status: "cancelled", Reason: "context_cancelled",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := app.resolveAgentRuntimeApprovalMessage(context.Background(), "", map[string]any{
		"approvalId": ids["approved"], "approve": true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.KernelMCPAuditTerminalStatus(context.Background(), "other-owner", identity.access.Frame.ID, "cancelled"); err != nil || found {
		t.Fatalf("foreign owner read terminal audit: found=%t err=%v", found, err)
	}
	if err := app.retireExpiredKernelMCPApprovals(context.Background(), identity.access); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"cancelled": "failed", "active": "pending", "approved": "approved"} {
		entry, _, err := app.runtimeStore.Get(agentRuntimeApprovalNamespace, ids[name])
		if err != nil || mapValue(entry.Value)["status"] != want {
			t.Fatalf("%s approval=%#v want=%s err=%v", name, entry, want, err)
		}
	}
}

func TestKernelMCPApprovalCancellationRetiresOnlyPendingRequest(t *testing.T) {
	app := New(Options{FileRoot: t.TempDir()})
	t.Cleanup(func() { closeTestServer(t, app) })
	if _, err := app.settingsStore.Set(approvalDefaultsSettingKey, map[string]any{"mode": "ask"}); err != nil {
		t.Fatal(err)
	}
	resolution := kernelMCPResolution{
		connector: workspaceMCPRuntimeConnector{ID: "custom:remote", Name: "remote", Source: "custom"},
		tool:      mcpstdio.ToolProjection{Name: "mcp__remote__echo", ToolName: "echo"},
	}
	permission := app.kernelMCPPermissionResult(context.Background(), "frame", resolution,
		agentruntime.ToolCall{ID: "cancelled-host-call", Name: resolution.tool.Name}, map[string]any{"text": "evidence"})
	approvalID := stringValue(permission["approvalId"])
	if approvalID == "" {
		t.Fatal("MCP approval was not queued")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.waitForKernelMCPApproval(ctx, approvalID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled approval wait=%v", err)
	}
	entry, found, err := app.runtimeStore.Get(agentRuntimeApprovalNamespace, approvalID)
	if err != nil || !found || stringValue(mapValue(entry.Value)["status"]) != "failed" {
		t.Fatalf("cancelled MCP approval remained pending: entry=%#v found=%t err=%v", entry, found, err)
	}
}

func TestKernelMCPApprovalPublishesAuthorityBeforeImmediateDecision(t *testing.T) {
	for _, approved := range []bool{true, false} {
		name := "denied"
		if approved {
			name = "approved"
		}
		t.Run(name, func(t *testing.T) {
			app := New(Options{FileRoot: t.TempDir()})
			t.Cleanup(func() { closeTestServer(t, app) })
			if _, err := app.settingsStore.Set(approvalDefaultsSettingKey, map[string]any{"mode": "ask"}); err != nil {
				t.Fatal(err)
			}
			resolution := kernelMCPResolution{
				connector: workspaceMCPRuntimeConnector{ID: "custom:remote", Name: "remote", Source: "custom"},
				tool:      mcpstdio.ToolProjection{Name: "mcp__remote__echo", ToolName: "echo"},
			}
			permission := app.kernelMCPPermissionResult(context.Background(), "frame", resolution,
				agentruntime.ToolCall{ID: "immediate-decision", Name: resolution.tool.Name}, map[string]any{"text": "evidence"})
			approvalID := stringValue(permission["approvalId"])
			if approvalID == "" {
				t.Fatalf("approval was not published: %#v", permission)
			}
			entry, found, err := app.runtimeStore.Get(agentRuntimeApprovalNamespace, approvalID)
			if err != nil || !found {
				t.Fatalf("approval found=%t err=%v", found, err)
			}
			// The first visible version must be complete. A second annotation
			// write admits a window where an immediate decision is overwritten.
			if entry.Version != 1 {
				t.Fatalf("approval authority was published in %d writes, want one atomic publication", entry.Version)
			}
			value := mapValue(entry.Value)
			for key, want := range map[string]string{
				"status": "pending", "approvalSource": "kernel-host-mcp", "frameId": "frame",
				"mcpServerId": "custom:remote", "mcpServer": "remote", "mcpTool": "echo", "kernelKind": "operon",
			} {
				if stringValue(value[key]) != want {
					t.Fatalf("first publication %s=%v want=%s", key, value[key], want)
				}
			}
			decision, err := app.resolveAgentRuntimeApprovalMessage(context.Background(), "", map[string]any{
				"approvalId": approvalID, "approve": approved,
			})
			if err != nil || stringValue(mapValue(decision)["status"]) != name {
				t.Fatalf("immediate decision=%#v err=%v", decision, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = app.waitForKernelMCPApproval(ctx, approvalID)
			if approved && err != nil || !approved && err == nil || ctx.Err() != nil {
				t.Fatalf("immediate %s was not retained: err=%v ctx=%v", name, err, ctx.Err())
			}
		})
	}
}
