package server

import (
	"net/http"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestAnnotationNoteNormalizerPreservesUTF16Boundary(t *testing.T) {
	for _, note := range []string{strings.Repeat("A", 4000), strings.Repeat("界", 4000), strings.Repeat("🧪", 2000), " "} {
		body, err := normalizeAnnotationBody(map[string]any{"type": "point", "text": note, "x_percent": float64(0), "y_percent": float64(0)})
		if err != nil || body["text"] != note {
			t.Fatalf("valid note was rejected or changed: units accepted, err=%v", err)
		}
	}
	for _, note := range []any{nil, 1, "", strings.Repeat("A", 4001), strings.Repeat("界", 4001), strings.Repeat("A", 3999) + "🧪", strings.Repeat("🧪", 2000) + "x"} {
		if _, err := normalizeAnnotationBody(map[string]any{"type": "point", "text": note, "x_percent": float64(0), "y_percent": float64(0)}); err == nil {
			t.Fatal("invalid or over-4000-UTF16 note was accepted")
		}
	}
}

func TestAnnotationNoteHTTPCreatesAndUpdatesExactUTF16Boundary(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: "note-boundary-document", ProjectID: f.stream.ProjectID, Name: "document.txt", Kind: "text", Content: []byte("Synthetic note target")})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/artifacts/" + artifact.ID + "/versions/" + version.ID + "/annotations"
	for _, note := range []string{strings.Repeat("A", 4000), strings.Repeat("界", 4000), strings.Repeat("🧪", 2000)} {
		created := p3JSONRequest(t, f.server, http.MethodPost, path, map[string]any{"type": "point", "text": note, "x_percent": float64(0), "y_percent": float64(0)}, f.stream.OwnerID)
		if created.Code != http.StatusCreated {
			t.Fatalf("create boundary HTTP%d", created.Code)
		}
		value := p3DecodeObject(t, created)
		if value["text"] != note {
			t.Fatal("create truncated or transformed the valid note")
		}
		id, ok := value["id"].(string)
		if !ok || id == "" {
			t.Fatal("create omitted note identity")
		}
		updated := p3JSONRequest(t, f.server, http.MethodPatch, "/api/annotations/"+id, map[string]any{"text": note}, f.stream.OwnerID)
		if updated.Code != http.StatusOK || p3DecodeObject(t, updated)["text"] != note {
			t.Fatalf("update rejected or changed a valid boundary: HTTP%d", updated.Code)
		}
		over := note + "x"
		rejected := p3JSONRequest(t, f.server, http.MethodPatch, "/api/annotations/"+id, map[string]any{"text": over}, f.stream.OwnerID)
		if rejected.Code != http.StatusBadRequest {
			t.Fatal("update accepted boundary+1")
		}
		rejected = p3JSONRequest(t, f.server, http.MethodPost, path, map[string]any{"type": "point", "text": over, "x_percent": float64(0), "y_percent": float64(0)}, f.stream.OwnerID)
		if rejected.Code != http.StatusBadRequest {
			t.Fatal("create accepted boundary+1")
		}
		stored, found, err := f.store.GetAnnotation(id)
		if err != nil || !found || stored.Body["text"] != note {
			t.Fatal("rejected update changed the persisted note")
		}
	}
}
