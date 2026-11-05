package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestWebWorkingDataLinkResolutionKeepsDeliveryAndScope(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	write := func(id, name, content string) workspace.ArtifactVersion {
		t.Helper()
		_, version, err := f.store.WriteArtifactVersion(t.Context(), workspace.WriteArtifactVersionInput{
			ArtifactID: id, ProjectID: f.stream.ProjectID, Name: name, ContentType: "text/plain",
			Content: strings.NewReader(content), MaxBytes: 1024,
			RootFrameID: f.stream.RootFrameID, FrameID: f.stream.FrameID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return version
	}
	old := write("source-link", "source.txt", "original source")
	current := write("source-link", "source.txt", "new source")
	report := write("report-link", "report.txt", "report")
	internal := write(runnerLargeToolResultArtifactIDPrefix+strings.Repeat("a", 32), "internal.txt", "internal")
	deleted := write("deleted-link", "deleted.txt", "deleted")
	if err := f.store.SetArtifactRetentionMode(t.Context(), "source-link", f.stream.ProjectID, f.stream.OwnerID, "working_data"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateFrame(workspace.CreateFrameInput{ID: "other-link-root", ProjectID: f.stream.ProjectID,
		AgentName: "OPERON", Status: "completed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteArtifact("deleted-link"); err != nil {
		t.Fatal(err)
	}
	request := func(t *testing.T, owner, frame string, body map[string]any, status int, want ...string) {
		t.Helper()
		response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+frame+"/artifacts", body, owner)
		if response.Code != status {
			t.Fatalf("reference status=%d want=%d body=%s", response.Code, status, response.Body.String())
		}
		if status != http.StatusOK {
			return
		}
		var collections []struct {
			Payload struct {
				Files []struct {
					VersionID string `json:"version_id"`
				} `json:"files"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &collections); err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, collection := range collections {
			for _, file := range collection.Payload.Files {
				got = append(got, file.VersionID)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("resolved versions=%v want=%v", got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("resolved versions=%v want=%v", got, want)
			}
		}
	}
	link := func(aid, vid string) map[string]any {
		return map[string]any{"intent": "resolve_link", "references": []any{map[string]any{"artifact_id": aid, "version_id": vid}}}
	}
	t.Run("default_hydration_stays_delivery_only", func(t *testing.T) {
		request(t, f.stream.OwnerID, f.stream.FrameID, map[string]any{"version_ids": []string{report.ID, old.ID, current.ID}}, http.StatusOK, report.ID)
	})
	t.Run("explicit_owned_old_working_version", func(t *testing.T) {
		request(t, f.stream.OwnerID, f.stream.FrameID, link("source-link", old.ID), http.StatusOK, old.ID)
	})
	t.Run("version_only_link", func(t *testing.T) {
		request(t, f.stream.OwnerID, f.stream.FrameID, map[string]any{"intent": "resolve_link", "version_ids": []string{current.ID}}, http.StatusOK, current.ID)
	})
	t.Run("wrong_owner", func(t *testing.T) {
		request(t, "foreign-link-owner", f.stream.FrameID, link("source-link", old.ID), http.StatusNotFound)
	})
	t.Run("wrong_root", func(t *testing.T) {
		request(t, f.stream.OwnerID, "other-link-root", link("source-link", old.ID), http.StatusOK)
	})
	t.Run("wrong_artifact_version_pair", func(t *testing.T) {
		request(t, f.stream.OwnerID, f.stream.FrameID, link("report-link", old.ID), http.StatusOK)
	})
	t.Run("internal_tool_data", func(t *testing.T) {
		request(t, f.stream.OwnerID, f.stream.FrameID, link(internal.ArtifactID, internal.ID), http.StatusOK)
	})
	t.Run("deleted_version", func(t *testing.T) {
		request(t, f.stream.OwnerID, f.stream.FrameID, link(deleted.ArtifactID, deleted.ID), http.StatusOK)
	})
	t.Run("unknown_intent", func(t *testing.T) {
		request(t, f.stream.OwnerID, f.stream.FrameID, map[string]any{"intent": "enumerate_all", "version_ids": []string{old.ID}}, http.StatusBadRequest)
	})
	if _, err := f.store.PruneArtifactVersionContentExcept(t.Context(), "source-link", current.ID, f.stream.ProjectID, f.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
	t.Run("pruned_old_version_never_falls_forward", func(t *testing.T) {
		request(t, f.stream.OwnerID, f.stream.FrameID, link("source-link", old.ID), http.StatusOK)
		request(t, f.stream.OwnerID, f.stream.FrameID, link("source-link", current.ID), http.StatusOK, current.ID)
	})
}
