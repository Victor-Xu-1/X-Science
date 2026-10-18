package server

import (
	"context"
	"net/http"
	"testing"
)

func TestManualContextCompactionPublicCapabilityAndAdmission(t *testing.T) {
	for _, action := range []string{"capability", "admission"} {
		t.Run(action, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			finishSkillControlInput(t, f)
			path := "/api/conversations/" + f.stream.FrameID + "/context-compactions"
			if action == "capability" {
				response := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID)
				if response.Code != http.StatusOK {
					t.Fatalf("capability HTTP %d: %s", response.Code, response.Body.String())
				}
				body := p3DecodeObject(t, response)
				target, ok := body["target"].(map[string]any)
				if body["schema"] != "x-science.context-compaction-snapshot.v1" || !ok ||
					target["frame_incarnation_id"] != f.identity.access.Frame.IncarnationID ||
					target["root_frame_incarnation_id"] != f.identity.access.RootFrameIncarnationID ||
					target["stream_uid"] != f.stream.UID {
					t.Fatalf("capability lacks real target: %#v", body)
				}
				return
			}
			request := manualContextCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
			response := p3JSONRequest(t, f.server, http.MethodPost, path, request, f.stream.OwnerID)
			if response.Code != http.StatusAccepted {
				t.Fatalf("admission HTTP %d: %s", response.Code, response.Body.String())
			}
			body := p3DecodeObject(t, response)
			if body["schema"] != "x-science.context-compaction-receipt.v1" || body["accepted"] != true || webString(body["input_message_id"]) == "" {
				t.Fatalf("admission did not return a canonical receipt: %#v", body)
			}
		})
	}
}

func manualContextCompactionRequestForTest(t *testing.T, f *agentSaveArtifactsFixture, requestID string) map[string]any {
	t.Helper()
	stream, err := f.repo.GetStream(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.repo.GetProjectionSnapshot(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"schema": "x-science.context-compaction-request.v1", "request_id": requestID,
		"expected": map[string]any{
			"frame_incarnation_id": f.identity.access.Frame.IncarnationID, "root_frame_id": f.stream.RootFrameID,
			"root_frame_incarnation_id": f.identity.access.RootFrameIncarnationID,
			"stream_uid":                stream.UID, "stream_epoch": stream.Epoch,
			"branch_id": snapshot.BranchID, "branch_generation": snapshot.BranchGeneration,
			"input_revision": stream.InputRevision, "through_publication_seq": snapshot.ThroughPublicationSequence,
		}}
}
