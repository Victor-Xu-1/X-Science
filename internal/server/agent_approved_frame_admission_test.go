package server

import (
	"context"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestUnprojectedApprovedCapabilityDoesNotTrustPersistedWorkspace(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "owner", "admission-project", "admission-frame")
	ungranted := t.TempDir()
	if _, err := store.UpdateProject("admission-project", workspace.UpdateProjectInput{Path: &ungranted}); err != nil {
		t.Fatal(err)
	}
	app := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { closeTestServer(t, app) })
	permission := app.resolveApprovedAgentToolContext(context.Background(), "admission-frame", requestHostAccessToolName)
	if permission == nil || permission.access.UserID != "owner" || permission.access.Frame.ID != "admission-frame" || permission.workspaceDir != "" {
		t.Fatalf("capability identity=%#v", permission)
	}
	compute := app.resolveApprovedAgentToolContext(context.Background(), "admission-frame", computeProviderToolName)
	if compute == nil || compute.workspaceDir == "" || hostPathWithin(ungranted, compute.workspaceDir) {
		t.Fatalf("compute admitted ungranted host workspace: %#v", compute)
	}
	want, err := app.defaultAgentKernelTaskWorkspace(compute.access.Frame.ProjectID, compute.access.Frame.RootFrameID, compute.access.RootFrameIncarnationID)
	if err != nil || compute.workspaceDir != want {
		t.Fatalf("compute workspace=%q want=%q err=%v", compute.workspaceDir, want, err)
	}
	if identity := app.resolveApprovedAgentToolContext(context.Background(), "admission-frame", "edit_file"); identity != nil {
		t.Fatalf("capability admission leaked into unprojected native file authority: %#v", identity)
	}
}

func TestApprovedCapabilityAdmissionRejectsInvalidFrameAuthority(t *testing.T) {
	for _, test := range []string{"missing frame", "foreign stream owner", "wrong stream project", "orphan stream", "noncanonical orphan stream", "unavailable transcript", "runner-bound context", "unknown runner binding", "unknown artifact binding", "cancelled context"} {
		t.Run(test, func(t *testing.T) {
			store, repo, db := newTranscriptWebFixture(t)
			seedTranscriptWebFrame(t, store, "owner", "admission-project", "admission-frame")
			app := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
			t.Cleanup(func() { closeTestServer(t, app) })
			frameID := "admission-frame"
			ctx := context.Background()
			switch test {
			case "missing frame":
				frameID = "not-a-durable-frame"
			case "runner-bound context":
				ctx = withTranscriptRunnerChatRun(ctx, &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{}})
			case "unknown runner binding":
				ctx = withTranscriptRunnerChatRun(ctx, &sessionRunnerChatRun{})
			case "unknown artifact binding":
				ctx = withTranscriptArtifactRun(ctx, &transcriptRunnerAuthority{}, 0)
			case "unavailable transcript":
				app.transcriptStore = &transcriptstore.Repository{}
			case "cancelled context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			default:
				owner, project, streamFrameID := "owner", "admission-project", frameID
				if test == "foreign stream owner" {
					owner = "foreign-owner"
				}
				if test == "foreign stream owner" || test == "wrong stream project" {
					project = "foreign-project"
					streamFrameID = "foreign-frame"
					seedTranscriptWebFrame(t, store, owner, project, streamFrameID)
				}
				streamUID := "frame:" + frameID
				if test == "noncanonical orphan stream" {
					streamUID = "imported-history:" + frameID
				}
				if _, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
					UID: streamUID, OwnerID: owner, ExternalID: streamFrameID, SessionID: streamFrameID,
					Kind: transcriptstore.StreamKindFrameRef, ProjectID: project, RootFrameID: streamFrameID, FrameID: streamFrameID, Epoch: 1,
				}); err != nil {
					t.Fatal(err)
				}
				if test == "orphan stream" || test == "noncanonical orphan stream" {
					if _, err := db.Exec(`DELETE FROM transcript_frame_authority WHERE owner_id=? AND session_id=?`, "owner", frameID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if identity := app.resolveApprovedAgentToolContext(ctx, frameID, requestHostAccessToolName); identity != nil {
				t.Fatalf("invalid authority gained approved capability identity: %#v", identity)
			}
		})
	}
}
