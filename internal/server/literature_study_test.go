package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	settingsstore "x-science/internal/persistence/settings"
	workspace "x-science/internal/persistence/workspace"
)

func serverStudyFixture(t *testing.T) (*literatureHTTPNumberFixture, workspace.LiteratureStudySource, workspace.LiteratureStudyGenerate) {
	t.Helper()
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	if _, err := f.f.store.PatchLiteratureItem(t.Context(), owner, f.itemID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Metadata: map[string]any{"abstract": "Synthetic abstract reporting an association."}}); err != nil {
		t.Fatal(err)
	}
	source, view, err := f.f.store.ReadLiteratureStudy(t.Context(), owner, f.itemID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	return f, source, workspace.LiteratureStudyGenerate{OperationID: uuid.NewString(), Language: "en-US", ExpectedSourceKey: source.Key, ExpectedRevision: view.Revision}
}
func TestLiteratureStudyReadRouteOwnerAndNoInference(t *testing.T) {
	f, source, _ := serverStudyFixture(t)
	path := "/api/literature/items/" + f.itemID + "/study-snapshot?language=en-US"
	response := p3JSONRequest(t, f.f.server, http.MethodGet, path, nil, f.f.stream.OwnerID)
	var view workspace.LiteratureStudyView
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &view) != nil || view.Snapshot != nil || view.SourceKey != source.Key {
		t.Fatal(response.Code, response.Body.String())
	}
	if response := p3JSONRequest(t, f.f.server, http.MethodGet, path, nil, "foreign-owner"); response.Code != 404 {
		t.Fatal("owner route", response.Code)
	}
	if response := p3JSONRequest(t, f.f.server, http.MethodGet, path+"&extra=bad", nil, f.f.stream.OwnerID); response.Code != 400 {
		t.Fatal("unknown query", response.Code)
	}
}
func TestLiteratureStudyGenerationPublicationAndCancellation(t *testing.T) {
	f, source, input := serverStudyFixture(t)
	owner := f.f.stream.OwnerID
	calls := 0
	evidence := func(context.Context, workspace.LiteratureStudySource) (literatureStudyEvidence, error) {
		return literatureStudyEvidence{}, errors.New("unreadable synthetic PDF")
	}
	infer := func(ctx context.Context, prompt string) (workspace.LiteratureStudySections, string, error) {
		calls++
		if !strings.Contains(prompt, `"coverage":"abstract"`) || !strings.Contains(prompt, "Synthetic abstract") {
			t.Error("missing honest fallback provenance")
		}
		question := "Synthetic question"
		return workspace.LiteratureStudySections{Question: &question}, "fake-no-network", nil
	}
	first, err := f.f.server.generateLiteratureStudy(t.Context(), owner, f.itemID, input, evidence, infer)
	if err != nil || first.Snapshot == nil || first.Snapshot.Coverage != "abstract" || calls != 1 {
		t.Fatal(first, err, calls)
	}
	replay, err := f.f.server.generateLiteratureStudy(t.Context(), owner, f.itemID, input, evidence, infer)
	if err != nil || replay.Revision != first.Revision || calls != 1 {
		t.Fatal("replay inferred again", replay, err, calls)
	}
	next := input
	next.OperationID = uuid.NewString()
	next.ExpectedRevision = first.Revision
	ctx, cancel := context.WithCancel(t.Context())
	_, err = f.f.server.generateLiteratureStudy(ctx, owner, f.itemID, next, evidence, func(context.Context, string) (workspace.LiteratureStudySections, string, error) {
		cancel()
		return workspace.LiteratureStudySections{}, "cancelled-fake", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancel did not fence", err)
	}
	_, retained, err := f.f.store.ReadLiteratureStudy(t.Context(), owner, f.itemID, input.Language)
	if err != nil || retained.Revision != first.Revision {
		t.Fatal("cancel overwrote ready", retained, err)
	}
	_, err = f.f.server.generateLiteratureStudy(t.Context(), owner, f.itemID, next, evidence, func(context.Context, string) (workspace.LiteratureStudySections, string, error) {
		title := "Changed during inference"
		_, patchErr := f.f.store.PatchLiteratureItem(t.Context(), owner, f.itemID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 2, Title: &title})
		if patchErr != nil {
			t.Fatal(patchErr)
		}
		return workspace.LiteratureStudySections{}, "fake", nil
	})
	if !errors.Is(err, workspace.ErrLiteratureStudySourceChanged) {
		t.Fatal("changed source published", source, err)
	}
}
func TestLiteratureStudyPDFImmutableCoverage(t *testing.T) {
	for _, tool := range []string{"pdftotext", "pdfinfo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("existing PDF extractor unavailable: " + tool)
		}
	}
	f, source, _ := serverStudyFixture(t)
	result, err := f.f.server.literatureStudyPDF(t.Context(), source, f.f.stream.OwnerID)
	if err != nil || result.Coverage != "full-text" || result.VersionID != source.Version.ID || !strings.Contains(result.Text, "first page") || !strings.Contains(result.Text, "second page") {
		t.Fatalf("original immutable reader/extractor: %+v %v", result, err)
	}
	if _, err := f.f.server.literatureStudyPDF(t.Context(), source, "foreign-owner"); err == nil {
		t.Fatal("foreign owner PDF read")
	}
}

func TestLiteratureStudyModelToolLessProfile(t *testing.T) {
	f, _, _ := serverStudyFixture(t)
	owner := f.f.stream.OwnerID
	var calls atomic.Int32
	var emitTool atomic.Bool
	f.f.server.settingsStore = settingsstore.New(filepath.Join(t.TempDir(), "study-settings.json"))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("request JSON")
		}
		if body["tool_choice"] != "none" || body["model"] != "synthetic-study-model" || body["tools"] != nil {
			t.Errorf("not tool-less: %+v", body)
		}
		message := map[string]any{"role": "assistant", "content": `{"question":null,"population":null,"methods":null,"results":null,"limitations":null}`}
		if emitTool.Load() {
			message["tool_calls"] = []any{map[string]any{"id": "forbidden-tool", "type": "function", "function": map[string]any{"name": "forbidden", "arguments": "{}"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "synthetic-study-model", "choices": []any{map[string]any{"finish_reason": "stop", "message": message}}})
	}))
	defer provider.Close()
	enabled := true
	profile, err := f.f.store.RegisterModelProvider(workspace.ModelProviderInput{ID: "study-provider", UserID: owner, Name: "Synthetic local provider", Type: "openai-compatible", BaseURL: provider.URL + "/v1", Model: "synthetic-study-model", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.f.server.settingsStore.Set("model.activeProviderId", profile.ID); err != nil {
		t.Fatal(err)
	}
	_, model, err := f.f.server.inferLiteratureStudy(t.Context(), owner, `{"abstract":"Synthetic fixture only"}`)
	if err != nil || model != "synthetic-study-model" || calls.Load() != 1 {
		t.Fatal(model, err, calls.Load())
	}
	emitTool.Store(true)
	if _, _, err := f.f.server.inferLiteratureStudy(t.Context(), owner, "synthetic tool violation"); err == nil {
		t.Fatal("tool call accepted")
	}
	before := calls.Load()
	if _, _, err := f.f.server.inferLiteratureStudy(t.Context(), "foreign-owner", "synthetic"); err == nil || calls.Load() != before {
		t.Fatal("foreign provider selected", err, calls.Load())
	}
}
