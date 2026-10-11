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
	err = server.admitAgentComputeProviderWorkspace(context.Background(), access, projectPath, func() error { return nil })
	if err != nil {
		t.Fatalf("persisted project path must hand off to the same admitted task workspace: %v", err)
	}
}
