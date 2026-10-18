package server

import (
	"context"
	"net/http"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestDelegatedStopTypedReadDoesNotUseWholeRootStop(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	branch, err := f.repo.GetBranchState(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/frames/" + f.stream.FrameID + "/delegated-stops?branch_id=" + branch.ActiveBranchID
	response := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("typed delegated-stop snapshot HTTP %d: %s", response.Code, response.Body.String())
	}
	if p3DecodeObject(t, response)["schema"] != "x-science.delegated-stop-snapshot.v1" {
		t.Fatalf("snapshot=%s", response.Body.String())
	}
	frame, found, err := f.store.GetFrame(f.stream.FrameID)
	if err != nil || !found || frame.Status != "completed" {
		t.Fatalf("read changed parent=%#v %v", frame, err)
	}
}

func TestDelegatedStopLineageDoesNotProjectCancellationAsSuccess(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	ctx := context.Background()
	children, err := f.store.CreateKernelSupervisedChildren(ctx, workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "lineage-stop-delegate",
		Requests: []workspace.KernelDelegateRequest{{Task: "Target child"}, {Task: "Unaffected sibling"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := f.store.StopKernelSupervisedChildTree(ctx, f.stream.FrameID, children[0].FrameID, f.stream.OwnerID, "Explicit child stop"); err != nil || !result.Applied {
		t.Fatalf("stop=%#v %v", result, err)
	}
	response := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+f.stream.FrameID+"/lineage", nil, f.stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("lineage HTTP %d: %s", response.Code, response.Body.String())
	}
	rows := p3DecodeObject(t, response)["directChildren"].([]any)
	seen := map[string]string{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		seen[webString(row["frameId"])] = webString(row["status"])
	}
	if seen[children[0].FrameID] != "stopped" || seen[children[1].FrameID] != "running" {
		t.Fatalf("cancelled child/sibling projection=%#v", seen)
	}
}
