package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestSessionPermissionGrantCanonicalApprovalListRevokeAndRetry(t *testing.T) {
	app, store, request, operation := newSessionPermissionOperationFixture(t)
	beforeRemembered, beforeFound, err := app.settingsStore.Get(approvalRememberedSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	// The canonical XScienceApprovalCard uses this existing direct resolver.
	memoryCompatJSON(t, app.Handler(), http.MethodPost, "/api/frames/"+request.FrameID+"/resolve-input", map[string]any{
		"responses": []any{map[string]any{"tool_id": request.RequestID, "action": "allow", "approved": true, "scope": "conversation"}},
	}, request.OwnerUserID, http.StatusOK)
	path := "/api/conversations/" + request.FrameID + "/permission-grants"
	snapshot := memoryCompatJSON(t, app.Handler(), http.MethodGet, path, nil, request.OwnerUserID, http.StatusOK)
	grants := snapshot["grants"].([]any)
	if len(grants) != 1 {
		t.Fatalf("actual approval did not appear: %#v", snapshot)
	}
	grant := grants[0].(map[string]any)
	if grant["kind"] != "local_exec" || grant["scope"] != "conversation" || grant["key"] != request.Tool {
		t.Fatalf("wrong permission=%#v", grant)
	}
	input := map[string]any{
		"schema": "x-science.session-permission-revoke.v1", "expected_frame_incarnation_id": request.FrameIncarnationID,
		"expected_root_frame_id": request.RootFrameID, "expected_root_frame_incarnation_id": request.RootFrameIncarnationID,
		"grants": []any{map[string]any{"grant_id": grant["grant_id"], "kind": grant["kind"], "key": grant["key"]}},
	}
	for index, want := range []string{"revoked", "already_absent"} {
		result := memoryCompatJSON(t, app.Handler(), http.MethodPost, path+"/revoke", input, request.OwnerUserID, http.StatusOK)
		rows := result["results"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["status"] != want || result["broader_policies_unchanged"] != true {
			t.Fatalf("attempt %d result=%#v", index, result)
		}
		if readback := result["snapshot"].(map[string]any); readback["schema"] != "x-science.session-permission-grants.v1" || len(readback["grants"].([]any)) != 0 {
			t.Fatalf("missing full mutation readback=%#v", result)
		}
	}
	policy, err := store.KernelLocalExecApprovalPolicy(context.Background(), request.OwnerUserID, request.ProjectID, request.RootFrameID, request.Tool, request.Environment)
	if err != nil || policy.Found {
		t.Fatalf("revoked permission still authorizes future input=%#v err=%v", policy, err)
	}
	afterRemembered, afterFound, err := app.settingsStore.Get(approvalRememberedSettingKey)
	if err != nil || beforeFound != afterFound || !reflect.DeepEqual(beforeRemembered, afterRemembered) {
		t.Fatal("session grant changed global remembered preferences")
	}
	admitted, found, err := store.GetKernelLocalOperation(context.Background(), request.OwnerUserID, operation.OperationID)
	if err != nil || !found || admitted.State != workspace.KernelLocalOperationStateApproved || admitted.ApprovalScope != "conversation" {
		t.Fatalf("revoke cancelled an already-admitted operation: %#v found=%t err=%v", admitted, found, err)
	}
}

func TestSessionPermissionGrantCompatibilityAdvertisesOnlySupportedScope(t *testing.T) {
	for _, test := range []struct {
		name, kind, tool string
		rememberable     bool
		want             bool
	}{
		{"local-exec", "local_exec", "python", true, true},
		{"software-once", "local_exec", softwareRuntimeToolName, true, false},
		{"nonrememberable", "local_exec", "python", false, false},
		{"generic-agent", "agent_tool", "python", true, false},
		{"artifact-delete", "artifact_delete", "delete", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := map[string]any{"kind": test.kind, "tool": test.tool, "tool_id": "request", "rememberable": test.rememberable}
			confirmation, ok := webPendingConfirmation(item)
			if !ok {
				t.Fatal("real projection rejected fixture")
			}
			advertised := false
			for _, option := range confirmation["options"].([]map[string]any) {
				if option["value"] == "proceed_conversation" {
					advertised = true
					if option["label"] != "Allow this session" {
						t.Fatal("unexpected translation key")
					}
				}
			}
			response, remembered, err := webConfirmationInputResponse("request", item, "proceed_conversation", false)
			if advertised != test.want || (err == nil) != test.want || remembered {
				t.Fatalf("advertised=%t response=%#v remember=%t err=%v", advertised, response, remembered, err)
			}
			if test.want && (response.Action != "allow" || response.Scope != "conversation") {
				t.Fatalf("wrong direct scope mapping=%#v", response)
			}
		})
	}
}

func newSessionPermissionApprovalFixture(t *testing.T) (*Server, *workspace.Store, workspace.KernelLocalExecApprovalRequestInput) {
	app, store, request, _ := newSessionPermissionFixture(t, true)
	return app, store, request
}

func newSessionPermissionFixture(t *testing.T, legacyPending bool) (*Server, *workspace.Store, workspace.KernelLocalExecApprovalRequestInput, transcriptstore.RunnerClaim) {
	t.Helper()
	store, repo, _ := newTranscriptWebFixture(t)
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "session-project", UserID: "owner", Name: "Session permissions"}); err != nil {
		t.Fatal(err)
	}
	frame, err := store.CreateFrame(workspace.CreateFrameInput{ID: "session-frame", ProjectID: "session-project", AgentName: "OPERON", Status: "processing", ConversationType: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{UID: "frame:" + frame.ID, OwnerID: "owner", ExternalID: frame.ID, SessionID: frame.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: frame.ID, FrameID: frame.ID, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "session-input", FrameEventID: "session-input-event", MessageUUID: "session-input", Text: "Inspect a small table.",
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "session-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	claimHash, inputHash := sha256.Sum256([]byte(claim.Claim.ClaimToken)), sha256.Sum256([]byte("session-permission-input"))
	request := workspace.KernelLocalExecApprovalRequestInput{OwnerUserID: "owner", ProjectID: frame.ProjectID, FrameID: frame.ID, FrameIncarnationID: frame.IncarnationID,
		RootFrameID: frame.ID, RootFrameIncarnationID: frame.IncarnationID, RequestID: "session-approval", Tool: "python", ToolCallID: "session-call", Environment: "science",
		InputSHA256: hex.EncodeToString(inputHash[:]), StreamUID: stream.UID, RunnerID: claim.Claim.RunnerID, RunnerAttempt: claim.Claim.Attempt, ClaimTokenSHA256: hex.EncodeToString(claimHash[:]),
		KernelID: "session-kernel", ExpectedGeneration: 1, Code: "print(1)"}
	if legacyPending {
		if _, err := store.AddKernelLocalExecApprovalRequest(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	app := New(Options{FileRoot: t.TempDir(), Workspace: store, Transcript: repo})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = app.Close(ctx)
	})
	return app, store, request, claim.Claim
}
