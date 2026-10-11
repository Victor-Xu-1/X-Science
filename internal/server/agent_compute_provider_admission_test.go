package server

import (
	"context"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func TestAdmitAgentComputeProviderWorkspaceMatchesManagedTaskRemap(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "owner-admit", "project-admit", "frame-admit")
	projectPath := t.TempDir()
	if _, err := store.UpdateProject("project-admit", workspace.UpdateProjectInput{Path: &projectPath}); err != nil {
		t.Fatal(err)
	}
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	access, found, err := store.GetKernelFrameAccessContext(context.Background(), "frame-admit")
	if err != nil || !found {
		t.Fatalf("frame access found=%t err=%v", found, err)
	}
	admitted, err := server.admitAgentWorkspace(access, access.ProjectPath)
	if err != nil || admitted == access.ProjectPath {
		t.Fatalf("ungranted project path must remap to managed task workspace: admitted=%q err=%v", admitted, err)
	}
	identity := &agentKernelContext{access: access, workspaceDir: admitted}
	err = server.admitAgentComputeProviderWorkspace(context.Background(), access, identity.workspaceDir, func() error { return nil })
	if err != nil {
		t.Fatalf("admitAgentComputeProviderWorkspace failed: %v", err)
	}

	fromRaw, err := server.admitAgentWorkspace(access, projectPath)
	if err != nil || fromRaw != admitted {
		t.Fatalf("raw project path admission=%q admitted=%q err=%v", fromRaw, admitted, err)
	}
	called := false
	err = server.admitAgentComputeProviderWorkspace(context.Background(), access, projectPath, func() error { called = true; return nil })
	if err == nil || called {
		t.Errorf("raw project path cannot stand in for the physical managed cwd: err=%v callback=%t", err, called)
	}
	for _, mutation := range []string{"revoke", "read_only"} {
		t.Run(mutation, func(t *testing.T) {
			if _, err := server.upsertHostGrant(access.UserID, projectPath, "read_write"); err != nil {
				t.Fatal(err)
			}
			physicalWorkspace, err := server.reconcileAgentComputeProviderWorkspace(access, projectPath)
			if err != nil || physicalWorkspace != projectPath {
				t.Fatalf("writable host plan=%q err=%v", physicalWorkspace, err)
			}
			if mutation == "revoke" {
				if removed, err := server.revokeHostGrant(access.UserID, projectPath); err != nil || !removed {
					t.Fatalf("revoke removed=%t err=%v", removed, err)
				}
			} else if _, err := server.upsertHostGrant(access.UserID, projectPath, "read"); err != nil {
				t.Fatal(err)
			}
			called := false
			err = server.admitAgentComputeProviderWorkspace(context.Background(), access, physicalWorkspace, func() error { called = true; return nil })
			if err == nil || called {
				t.Errorf("withdrawn host plan reached physical handoff: err=%v callback=%t", err, called)
			}
			canonical, err := server.reconcileAgentComputeProviderWorkspace(access, physicalWorkspace)
			if err != nil || canonical != admitted {
				t.Fatalf("canonical managed plan=%q want=%q err=%v", canonical, admitted, err)
			}
			called = false
			err = server.admitAgentComputeProviderWorkspace(context.Background(), access, canonical, func() error { called = true; return nil })
			if err != nil || !called {
				t.Fatalf("canonical managed handoff was rejected: err=%v callback=%t", err, called)
			}
		})
	}
}
