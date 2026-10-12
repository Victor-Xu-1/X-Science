package server

import (
	"context"
	"testing"

	workspace "x-science/internal/persistence/workspace"
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
	managed, err := server.reconcileAgentComputeProviderWorkspace(access, projectPath)
	if err != nil || managed == projectPath {
		t.Fatalf("reconcile(raw host path) must remap to managed task workspace: managed=%q err=%v", managed, err)
	}
	called := false
	err = server.admitAgentComputeProviderWorkspace(context.Background(), access, managed, func() error { called = true; return nil })
	if err != nil || !called {
		t.Fatalf("admit(exact managed cwd) failed: err=%v callback=%t", err, called)
	}
	called = false
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
			if err != nil || canonical != managed {
				t.Fatalf("canonical managed plan=%q want=%q err=%v", canonical, managed, err)
			}
			called = false
			err = server.admitAgentComputeProviderWorkspace(context.Background(), access, canonical, func() error { called = true; return nil })
			if err != nil || !called {
				t.Fatalf("canonical managed handoff was rejected: err=%v callback=%t", err, called)
			}
		})
	}
}
