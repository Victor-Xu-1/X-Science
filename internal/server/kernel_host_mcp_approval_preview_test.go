package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	"synon-go/internal/tools/mcpstdio"
)

func TestKernelMCPApprovalPreviewRedactsStructuredCredentials(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	if _, err := app.settingsStore.Set(approvalDefaultsSettingKey, map[string]any{"mode": "ask"}); err != nil {
		t.Fatal(err)
	}
	resolution := kernelMCPResolution{
		connector: workspaceMCPRuntimeConnector{ID: "custom:remote", Name: "remote", Source: "custom"},
		tool:      mcpstdio.ToolProjection{Name: "mcp__remote__echo", ToolName: "echo", ReadOnlyHint: true},
	}
	input := map[string]any{
		"text": "STAT6 non-sensitive input", "password": "fake-password-A", "api_key": "fake-api-key-B",
		"headers":        map[string]any{"Authorization": "Basic fake-authorization-C", "Cookie": "fake-cookie-D"},
		"nested":         []any{map[string]any{"clientSecret": "fake-client-secret-E", "access_token": "fake-access-token-F"}},
		"typed":          map[string]string{"password": "fake-typed-secret-G"},
		"body_json":      `{"password":"fake-json-secret-H","target":"STAT6"}`,
		"url":            "https://fake-user-I:fake-url-secret-J@public.example/query?access_token=fake-query-secret-K&species=human",
		"header_entries": []any{map[string]any{"name": "Authorization", "value": "fake-record-secret-L"}},
		"token_count":    42,
		"large_integer":  json.Number("2026000000000000001"),
	}
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	permission := app.kernelMCPPermissionResult(context.Background(), identity.access.Frame.ID, resolution,
		agentruntime.ToolCall{ID: "preview-host-call", Name: resolution.tool.Name}, input)
	approvalID := stringValue(permission["approvalId"])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		returned <- app.requireVisibleKernelMCPApproval(ctx, identity.access, resolution, input, permission)
	}()
	pending := waitKernelMCPPublicInput(t, store, identity.access.Frame.ID, approvalID)
	preview := stringValue(pending["code"])
	for _, secret := range []string{"fake-password-A", "fake-api-key-B", "fake-authorization-C", "fake-cookie-D", "fake-client-secret-E", "fake-access-token-F",
		"fake-typed-secret-G", "fake-json-secret-H", "fake-user-I", "fake-url-secret-J", "fake-query-secret-K", "fake-record-secret-L"} {
		if strings.Contains(preview, secret) {
			t.Errorf("structured credential was published in the pending input: %s", secret)
		}
	}
	for _, core := range []string{"STAT6 non-sensitive input", "2026000000000000001", "remote", "echo", "public.example", "species=human", "token_count"} {
		if !strings.Contains(preview, core) {
			t.Errorf("core approval input was lost: %s", core)
		}
	}
	after, err := json.Marshal(input)
	if err != nil || string(before) != string(after) {
		t.Fatal("preview sanitization changed the actual execution arguments")
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("preview-only call did not cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preview-only approval did not settle")
	}
}

func TestKernelMCPApprovalPreviewIsBoundedAndNonMutating(t *testing.T) {
	resolution := kernelMCPResolution{connector: workspaceMCPRuntimeConnector{Name: "remote"}, tool: mcpstdio.ToolProjection{ToolName: "echo"}}
	input := map[string]any{"password": "fake-top-secret", "text": strings.Repeat("界", 6000)}
	preview, err := kernelMCPApprovalPreview(resolution, input)
	if err != nil || len([]rune(preview)) > 4200 || !strings.Contains(preview, "Preview shortened") || strings.Contains(preview, "fake-top-secret") {
		t.Fatalf("unsafe/unbounded approval preview: len=%d err=%v", len([]rune(preview)), err)
	}
	if input["password"] != "fake-top-secret" || len([]rune(stringValue(input["text"]))) != 6000 {
		t.Fatal("preview changed actual MCP arguments")
	}
}
