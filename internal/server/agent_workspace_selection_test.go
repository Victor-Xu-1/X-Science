package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestHTTPManagedProjectInputsShareIsolatedRuntimeTaskWorkspace(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	app.transcriptStore = repo
	created := p3JSONRequest(t, app, http.MethodPost, "/api/go/projects", map[string]any{
		"id": "managed-input-project", "name": "Managed inputs", "path": "",
	}, "local")
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var project struct {
		Project struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"project"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &project); err != nil || project.Project.Path == "" {
		t.Fatalf("create body=%s err=%v", created.Body.String(), err)
	}

	var workdirs []string
	for _, frameID := range []string{"input-root-a", "input-root-b"} {
		frame, err := store.CreateFrame(workspace.CreateFrameInput{
			ID: frameID, ProjectID: project.Project.ID, AgentName: "OPERON",
			Status: "completed", ConversationType: "agent", Name: "Input task",
		})
		if err != nil {
			t.Fatal(err)
		}
		access, found, err := store.GetKernelFrameAccessContext(context.Background(), frame.ID)
		if err != nil || !found {
			t.Fatalf("access found=%v err=%v", found, err)
		}
		if _, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
			UID: "frame:" + frame.ID, OwnerID: access.UserID, ExternalID: frame.ID, SessionID: frame.ID,
			Kind: transcriptstore.StreamKindFrameRef, ProjectID: access.Frame.ProjectID,
			RootFrameID: access.Frame.RootFrameID, FrameID: frame.ID, Epoch: 1,
		}); err != nil {
			t.Fatal(err)
		}
		compat, found, err := store.GetCompatibilityFrame(frame.ID)
		if err != nil || !found {
			t.Fatalf("frame found=%v err=%v", found, err)
		}
		source := filepath.Join(t.TempDir(), "measurements.csv")
		want := []byte("task,value\n" + frameID + ",1.25\n")
		if err := os.WriteFile(source, want, 0o600); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.RemoteAddr = "127.0.0.1:12345"
		files, err := app.materializeWebConversationInputFiles(context.Background(), request, compat, []string{source})
		if err != nil || len(files) != 1 {
			t.Fatalf("materialize files=%v err=%v", files, err)
		}
		if files[0] != "inputs/measurements.csv" {
			t.Errorf("managed input was not copied into the task: %q", files[0])
		}
		_, workdir, authorized := app.resolveAgentWorkspaceAuthority(context.Background(), frame.ID)
		wantRoot, err := app.defaultAgentKernelTaskWorkspace(access.Frame.ProjectID, access.Frame.RootFrameID, access.RootFrameIncarnationID)
		if err != nil {
			t.Fatal(err)
		}
		if !authorized || workdir != wantRoot {
			t.Errorf("runtime workdir=%q authorized=%v want=%q", workdir, authorized, wantRoot)
		}
		workdirs = append(workdirs, workdir)
		got, err := os.ReadFile(filepath.Join(workdir, filepath.FromSlash(files[0])))
		if err != nil || string(got) != string(want) {
			t.Errorf("runtime cannot read attachment bytes=%q err=%v want=%q", got, err, want)
		}
		original, err := os.ReadFile(source)
		if err != nil || string(original) != string(want) {
			t.Fatalf("source changed bytes=%q err=%v", original, err)
		}
	}
	if workdirs[0] == workdirs[1] {
		t.Fatalf("independent root tasks share cwd=%q", workdirs[0])
	}
}

func TestApprovedWorkspaceEditRechecksLiveHostGrantWithoutKernel(t *testing.T) {
	for _, mode := range []string{"read_write", "revoked", "read"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newAgentSaveArtifactsFixture(t)
			app := fixture.server
			if err := disableBundledMCPForLocalFixture(context.Background(), app, fixture.stream.OwnerID); err != nil {
				t.Fatal(err)
			}
			if app.kernelManager != nil {
				t.Fatal("fixture must exercise the approved host-side fallback without a kernel manager")
			}
			target := filepath.Join(fixture.projectPath, "approved.txt")
			if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			const approvalID = "workspace-edit-approval"
			if _, err := app.runtimeStore.Set(agentRuntimeApprovalNamespace, approvalID, map[string]any{
				"status": "pending", "sessionId": fixture.stream.SessionID,
				"tool": "edit_file", "toolCallId": "approved-workspace-edit",
				"approvalSource": "agent-runtime", "rememberable": false,
				"input": map[string]any{"file_path": target, "old_string": "original", "new_string": "approved"},
			}); err != nil {
				t.Fatal(err)
			}
			if mode == "revoked" {
				if _, err := app.revokeHostGrant(fixture.stream.OwnerID, fixture.projectPath); err != nil {
					t.Fatal(err)
				}
			} else if mode == "read" {
				if _, err := app.upsertHostGrant(fixture.stream.OwnerID, fixture.projectPath, mode); err != nil {
					t.Fatal(err)
				}
			}
			resolved, err := app.executeSendMessageTool(context.Background(), map[string]any{
				"to": "agent-runtime", "message": map[string]any{
					"type": "agent_runtime_approval_response", "approvalId": approvalID,
					"approve": true, "reason": "Exercise the current workspace authority",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			result := mapValue(resolved)
			want, status := "original", "failed"
			if mode == "read_write" {
				want, status = "approved", "completed"
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != want || result["status"] != status {
				t.Fatalf("approval result=%#v host bytes=%q err=%v want=%q status=%q", result, got, err, want, status)
			}
		})
	}
}

func TestGrantedRepositoryInputPreservesExplicitPathContract(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	frame, found, err := fixture.store.GetCompatibilityFrame(fixture.stream.FrameID)
	if err != nil || !found {
		t.Fatalf("frame found=%v err=%v", found, err)
	}
	source := filepath.Join(fixture.projectPath, "repository-input.csv")
	if err := os.WriteFile(source, []byte("repository,value\nowned,1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	files, err := fixture.server.materializeWebConversationInputFiles(context.Background(), request, frame, []string{source})
	if err != nil || len(files) != 1 || files[0] != source {
		t.Fatalf("explicit repository files=%v err=%v", files, err)
	}
	_, cwd, authorized := fixture.server.resolveAgentWorkspaceAuthority(context.Background(), fixture.stream.SessionID)
	if !authorized || cwd != fixture.projectPath {
		t.Fatalf("explicit repository cwd=%q authorized=%v", cwd, authorized)
	}
}
