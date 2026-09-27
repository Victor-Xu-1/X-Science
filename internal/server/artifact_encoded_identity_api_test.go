package server

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

type encodedArtifactIdentity struct {
	name, artifactID, versionID string
}

func TestArtifactEncodedIdentityHTTP(t *testing.T) {
	identities := []encodedArtifactIdentity{
		{"ordinary", "artifact-plain", "version-plain"},
		{"space", "artifact with space", "version with space"},
		{"unicode", "产物-研究", "版本-一"},
		{"slash", "artifact/with space", "version/with space"},
		{"literal_escape", "artifact%2Fwith space", "version%2Fwith space"},
		{"literal_percent", "artifact%literal", "version%literal"},
		{"punctuation", "artifact?:#+&", "version?:#+&"},
		{"opaque_dot_path", "artifact/../report", "version/./one"},
		{"backslash", `artifact\report`, `version\one`},
	}
	app := encodedArtifactFixture(t, identities)
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for index, identity := range identities {
		t.Run(identity.name, func(t *testing.T) {
			artifactPath := "/api/artifacts/" + url.PathEscape(identity.artifactID)
			versionPath := "/api/artifacts/versions/" + url.PathEscape(identity.versionID)
			exactURL := agentArtifactURLs(identity.artifactID, identity.versionID)["content_url"].(string)
			for _, target := range []string{exactURL, artifactPath, versionPath} {
				status, headers, content := encodedArtifactHTTPRequest(t, client, server.URL, "owner", target)
				if status != http.StatusOK || string(content) != identity.name {
					t.Fatalf("content %q = %d %q", target, status, content)
				}
				if headers.Get("X-Artifact-Id") != identity.artifactID || headers.Get("X-Artifact-Version-Id") != identity.versionID {
					t.Fatalf("content identity changed: %v", headers)
				}
				for _, userID := range []string{"foreign", ""} {
					status, _, _ := encodedArtifactHTTPRequest(t, client, server.URL, userID, target)
					if status != http.StatusNotFound && status != http.StatusUnauthorized {
						t.Fatalf("unowned content %q = %d", target, status)
					}
				}
			}
			for _, target := range []string{
				exactURL + "?include_metadata=1", artifactPath + "?include_metadata=1", versionPath + "?include_metadata=1",
				artifactPath + "/metadata", artifactPath + "/versions",
				artifactPath + "/lineage?slim=1", versionPath + "/lineage?slim=1",
			} {
				status, _, body := encodedArtifactHTTPRequest(t, client, server.URL, "owner", target)
				if status != http.StatusOK || !json.Valid(body) {
					t.Fatalf("metadata %q = %d %s", target, status, body)
				}
				assertEncodedArtifactMetadataIdentity(t, body, identity)
				for _, userID := range []string{"foreign", ""} {
					status, _, _ := encodedArtifactHTTPRequest(t, client, server.URL, userID, target)
					if status != http.StatusNotFound && status != http.StatusUnauthorized {
						t.Fatalf("unowned metadata %q = %d", target, status)
					}
				}
			}
			wrongPair := agentArtifactURLs(identities[(index+1)%len(identities)].artifactID, identity.versionID)["content_url"].(string)
			if status, _, _ := encodedArtifactHTTPRequest(t, client, server.URL, "owner", wrongPair); status != http.StatusNotFound {
				t.Fatalf("wrong exact pair = %d", status)
			}
		})
	}
}

func assertEncodedArtifactMetadataIdentity(t *testing.T, body []byte, identity encodedArtifactIdentity) {
	t.Helper()
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	if versions, ok := value.([]any); ok {
		if len(versions) != 1 {
			t.Fatalf("expected one version, got %d", len(versions))
		}
		value = versions[0]
	}
	metadata, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected metadata object, got %s", body)
	}
	artifactID := metadata["artifact_id"]
	if artifactID == nil {
		artifactID = metadata["id"]
	}
	if artifactID != identity.artifactID || metadata["version_id"] != identity.versionID {
		t.Fatalf("metadata lost exact artifact/version identity: %s", body)
	}
}

func TestArtifactEncodedIdentityRejectsUnsafePaths(t *testing.T) {
	identity := encodedArtifactIdentity{"protected", "artifact-target", "version-target"}
	app := encodedArtifactFixture(t, []encodedArtifactIdentity{identity})
	for _, segment := range []string{"%2E", "%2e%2e", "%00", "%0A", "%7f", "%C2%85", "%FF", "%20"} {
		for _, target := range []string{
			"/api/artifacts/" + segment + "/versions/version-target",
			"/api/artifacts/artifact-target/versions/" + segment,
			"/api/artifacts/versions/" + segment,
		} {
			response := artifactCompatibilityRequest(t, app, "owner", target)
			if response.Code != http.StatusBadRequest {
				t.Errorf("unsafe identity %q = %d %s", target, response.Code, response.Body.String())
			}
		}
	}
	for _, target := range []string{
		"/api/artifacts/unused/../artifact-target/versions/version-target",
		"/api/artifacts/artifact-target/versions/./version-target",
		"/api/artifacts/artifact-target//versions/version-target",
		"/api/artifacts/artifact-target/versions/version-target/",
	} {
		response := artifactCompatibilityRequest(t, app, "owner", target)
		// ServeMux canonicalizes raw dot/empty path segments by redirecting before
		// dispatch. They must never be interpreted directly as an exact identity.
		if response.Code != http.StatusNotFound && response.Code != http.StatusTemporaryRedirect && response.Code != http.StatusMovedPermanently {
			t.Errorf("structural path %q = %d %s", target, response.Code, response.Body.String())
		}
	}
}

func TestArtifactEncodedIdentityFolderDispatch(t *testing.T) {
	identity := encodedArtifactIdentity{"folder", "artifact/folder%literal", "version/folder%literal"}
	app := encodedArtifactFixture(t, []encodedArtifactIdentity{identity})
	for _, userID := range []string{"owner", "foreign"} {
		request := httptest.NewRequest(http.MethodPatch, "/api/artifacts/"+url.PathEscape(identity.artifactID)+"/folder", strings.NewReader(`{"folder_id":null}`))
		request.Header.Set("X-Synon-User-Id", userID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		wantStatus := http.StatusOK
		if userID == "foreign" {
			wantStatus = http.StatusNotFound
		}
		if response.Code != wantStatus {
			t.Fatalf("folder %s = %d %s", userID, response.Code, response.Body.String())
		}
	}
}

func TestArtifactPathIdentityDecodeOnce(t *testing.T) {
	for _, identity := range []string{"a/b", "a%2Fb", "a%", "a/../b", `a\b`, "产物", " a "} {
		decoded, err := decodeArtifactPathIdentity(url.PathEscape(identity))
		if err != nil || decoded != identity {
			t.Errorf("round trip %q = %q, %v", identity, decoded, err)
		}
	}
	for _, encoded := range []string{"", "%", "%GG", "%ff", "%2e", "%2e%2e", "%0a", "%C2%85", "%20"} {
		if _, err := decodeArtifactPathIdentity(encoded); err == nil {
			t.Errorf("accepted invalid identity %q", encoded)
		}
	}
}

func encodedArtifactFixture(t *testing.T, identities []encodedArtifactIdentity) http.Handler {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "workspace.db")
	store, err := workspace.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, identity := range identities {
		_, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
			ArtifactID: identity.artifactID, ProjectID: "project", Name: "report.txt", Kind: "text/plain",
			Content: []byte(identity.name), CreatedBy: "agent",
		})
		if err != nil {
			t.Fatal(err)
		}
		// Normal writes generate UUID version IDs. Seed the persisted opaque-ID
		// contract directly to test HTTP decoding without mocking storage or routing.
		if _, err := db.Exec("UPDATE artifact_versions SET id = ? WHERE id = ?", identity.versionID, version.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO artifact_runtime_metadata (artifact_id, latest_version_id) VALUES (?, ?)", identity.artifactID, identity.versionID); err != nil {
			t.Fatal(err)
		}
	}
	return New(Options{Workspace: store}).Handler()
}

func encodedArtifactHTTPRequest(t *testing.T, client *http.Client, origin, userID, target string) (int, http.Header, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, origin+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Synon-User-Id", userID)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, body
}
