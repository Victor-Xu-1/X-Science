package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtifactTextVersionRejectsStaleExpectedHeadWithoutMutation(t *testing.T) {
	fixture := newArtifactReadFixture(t)
	// The editor opened v1, then another publication advanced the current head
	// to v2. Its parent records lineage; expected_head_version_id records the
	// separate current-version baseline that the user actually confirmed.
	body := `{"content":"stale draft","parent_version_id":"` + fixture.firstVersionID + `","expected_head_version_id":"` + fixture.firstVersionID + `"}`
	request := newLoopbackTestRequest(http.MethodPost, "/api/artifacts/"+fixture.artifactID+"/versions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", "owner-a")
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Errorf("stale editor save = HTTP%d, want409 without publication", response.Code)
	}
	var conflict struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Code != "ARTIFACT_HEAD_CONFLICT" || conflict.Details["current_head_version_id"] != fixture.secondVersionID || conflict.Details["expected_head_version_id"] != fixture.firstVersionID {
		t.Fatalf("unstructured conflict: %+v", conflict)
	}
	page := artifactCompatibilityRequest(t, fixture.handler, "owner-a", "/api/artifacts/"+fixture.artifactID+"/versions")
	if page.Code != http.StatusOK {
		t.Fatalf("history read = HTTP%d", page.Code)
	}
	var versions []map[string]any
	if err := json.Unmarshal(page.Body.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Errorf("stale editor created a version: history count=%d, want2", len(versions))
	}
}

func TestArtifactTextVersionValidatesPresentExpectedHead(t *testing.T) {
	fixture := newArtifactReadFixture(t)
	for _, value := range []string{`null`, `""`, `"   "`, `12`, `true`, `{}`} {
		t.Run(value, func(t *testing.T) {
			request := newLoopbackTestRequest(http.MethodPost, "/api/artifacts/"+fixture.artifactID+"/versions", strings.NewReader(`{"content":"new","expected_head_version_id":`+value+`}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Science-User-Id", "owner-a")
			response := httptest.NewRecorder()
			fixture.handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid expected head accepted: HTTP%d", response.Code)
			}
		})
	}
}
