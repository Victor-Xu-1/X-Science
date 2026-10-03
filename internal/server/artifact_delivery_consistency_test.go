package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func deliveryCollection(t *testing.T, f *agentSaveArtifactsFixture) workspace.CompatibilityProjectArtifactPage {
	t.Helper()
	page, err := f.store.ListCompatibilityProjectCurrentArtifactPage(context.Background(), workspace.CompatibilityProjectArtifactPageInput{
		OwnerUserID: f.stream.OwnerID, ProjectID: f.stream.ProjectID, FrameID: f.stream.FrameID,
		ExcludeIntermediate: true, ExcludeInternal: true, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestArtifactDeliveryCollectionsRecoverOnlyCompletedGeneratedHeads(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	old := saveRoundPresentationFile(t, f, "old-head", "result.txt", "superseded bytes")
	latest := saveRoundPresentationFile(t, f, "latest-head", "result.txt", "final bytes")
	other := saveRoundPresentationFile(t, f, "companion", "data.txt", "final companion")
	if page := deliveryCollection(t, f); page.Total != 0 {
		t.Fatalf("unfinished drafts visible: %#v", page)
	}
	finishRoundPresentation(t, f, "completed")
	page := deliveryCollection(t, f)
	if page.Total != 2 {
		t.Fatalf("completed delivery count=%d want=2", page.Total)
	}
	versions := map[string]bool{}
	for _, artifact := range page.Artifacts {
		versions[artifact.VersionID] = true
		if artifact.IsIntermediate {
			t.Fatal("collection returned a completed artifact as intermediate")
		}
	}
	if versions[stringValue(old["version_id"])] || !versions[stringValue(latest["version_id"])] || !versions[stringValue(other["version_id"])] {
		t.Fatalf("wrong immutable delivery heads: %#v", versions)
	}
	legacy, err := f.store.ListCompatibilityProjectArtifacts(context.Background(), f.stream.OwnerID, f.stream.ProjectID, true, 100)
	if err != nil || len(legacy) != 2 || legacy[0].IsIntermediate || legacy[1].IsIntermediate {
		t.Fatalf("legacy project library differs: %#v %v", legacy, err)
	}
	conversation, err := f.store.ListCompatibilityConversationArtifacts(context.Background(), f.stream.OwnerID, f.stream.ProjectID, f.stream.RootFrameID, true)
	if err != nil || len(conversation) != 2 {
		t.Fatalf("conversation collection differs: %#v %v", conversation, err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/conversations/frame-save/workspace?path=project-files", nil)
	response := httptest.NewRecorder()
	f.server.handleWebWorkspaceList(response, request, f.stream.OwnerID, f.stream.ProjectID, f.stream.FrameID)
	var body struct {
		Total int `json:"total"`
		Items []struct {
			VersionID string `json:"version_id"`
		} `json:"items"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Total != 2 || len(body.Items) != 2 {
		t.Fatalf("workspace HTTP delivery=%d %s", response.Code, response.Body.String())
	}
	for _, item := range body.Items {
		if !versions[item.VersionID] {
			t.Fatalf("HTTP library substituted another version: %q", item.VersionID)
		}
	}
	startNextPresentationRound(t, f)
	saveRoundPresentationFile(t, f, "new-draft", "result.txt", "uncompleted next input")
	page = deliveryCollection(t, f)
	if page.Total != 1 || page.Artifacts[0].VersionID != stringValue(other["version_id"]) {
		t.Fatalf("later unfinished version inherited old completion: %#v", page)
	}
}

func TestArtifactDeliveryDoesNotPublishFailureFromUnrelatedCompletedInput(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			saveRoundPresentationFile(t, f, "failed-output", "partial.txt", "kept for recovery")
			finishRoundPresentation(t, f, status)
			startNextPresentationRound(t, f)
			finishRoundPresentation(t, f, "completed")
			if page := deliveryCollection(t, f); page.Total != 0 {
				t.Fatalf("unrelated completed input exposed failed draft: %#v", page)
			}
		})
	}
}

func TestArtifactDeliveryProjectionDoesNotCrossOwnerOrAbandonedBranch(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	artifact := saveRoundPresentationFile(t, f, "branch-result", "result.txt", "completed original branch")
	finishRoundPresentation(t, f, "completed")
	if page := deliveryCollection(t, f); page.Total != 1 {
		t.Fatalf("initial delivery=%#v", page)
	}
	page, err := f.store.ListCompatibilityProjectCurrentArtifactPage(context.Background(), workspace.CompatibilityProjectArtifactPageInput{
		OwnerUserID: "other-owner", ProjectID: f.stream.ProjectID, ExcludeIntermediate: true, Limit: 100,
	})
	if err != nil || page.Total != 0 {
		t.Fatalf("foreign-owner delivery=%#v err=%v", page, err)
	}
	// Switch to a branch ending before the terminal, preserving all immutable
	// receipts. A completion outside that branch must not expose the draft.
	var source int64
	if err := f.db.QueryRow(`SELECT source_event_id FROM transcript_artifact_commits WHERE stream_uid=? AND version_id=? AND relation='produced'`, f.stream.UID, stringValue(artifact["version_id"])).Scan(&source); err != nil {
		t.Fatal(err)
	}
	forkRoundPresentationBeforeTerminal(t, f, source)
	if page := deliveryCollection(t, f); page.Total != 0 {
		t.Fatalf("abandoned terminal exposed delivery: %#v", page)
	}
	if _, intermediate, found, err := f.store.ArtifactCurrentPresentationState(stringValue(artifact["artifact_id"])); err != nil || !found || !intermediate {
		t.Fatalf("abandoned draft state=%t found=%t err=%v", intermediate, found, err)
	}
}
