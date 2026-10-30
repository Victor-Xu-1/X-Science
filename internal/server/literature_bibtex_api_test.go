package server

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureBibtexHTTPExportsCanonicalOwnedSelection(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	ids := []string{}
	for index, title := range []string{"Unicode 文献 {A \\ B\nnext", "Second reference"} {
		metadata := map[string]any{}
		if index == 0 {
			metadata = map[string]any{"item_type": "journalArticle", "authors": []string{"Ada Lovelace", "研究组"}, "year": json.Number("2024"), "doi": "10.1234/test0", "container_title": "Journal"}
		}
		receipt, err := f.f.store.CreateManualLiterature(t.Context(), owner, workspace.LiteratureManualCreate{
			OperationID: uuid.NewString(), Title: title, Metadata: metadata,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, receipt.Item.ID)
	}
	response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-bibtex", map[string]any{"item_ids": ids}, owner)
	var result struct {
		Schema  string   `json:"schema"`
		ItemIDs []string `json:"item_ids"`
		Bibtex  string   `json:"bibtex"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Schema != "x-science.literature-bibtex.v1" || len(result.ItemIDs) != 2 || result.ItemIDs[0] != ids[0] || result.ItemIDs[1] != ids[1] {
		t.Fatalf("canonical export HTTP%d %s", response.Code, response.Body.String())
	}
	for _, want := range []string{`Unicode 文献 \textbraceleft{}A \textbackslash{} B next`, "author = {{Ada Lovelace} and {研究组}}", "year = {2024}", "doi = {10.1234/test0}", "Second reference"} {
		if !strings.Contains(result.Bibtex, want) {
			t.Fatalf("missing %q: %s", want, result.Bibtex)
		}
	}
	if strings.Count(result.Bibtex, "@misc{") != 1 || strings.Count(result.Bibtex, "@article{") != 1 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("export lost entry identity or privacy")
	}
	for _, id := range ids {
		if !strings.Contains(result.Bibtex, "{x_"+hex.EncodeToString([]byte(id))+",") {
			t.Fatal("citation key lost canonical identity")
		}
	}
	if strings.Count(result.Bibtex, "author =") != 1 || strings.Count(result.Bibtex, "year =") != 1 || strings.Count(result.Bibtex, "doi =") != 1 || !strings.Contains(result.Bibtex, "journal = {Journal}") {
		t.Fatal("formatter invented missing fields or lost known article type")
	}
	replay := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-bibtex", map[string]any{"item_ids": ids}, owner)
	if response.Body.String() != replay.Body.String() {
		t.Fatal("read-only export changed stable keys/content on repeat")
	}
}

func TestLiteratureBibtexHTTPNeverReturnsPartialForeignMissingOrTrash(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	foreign, err := f.f.store.CreateManualLiterature(t.Context(), uuid.NewString(), workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Foreign reference"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{uuid.NewString(), foreign.Item.ID} {
		response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-bibtex", map[string]any{"item_ids": []string{f.itemID, bad}}, owner)
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "bibtex") {
			t.Fatalf("partial/foreign export HTTP%d %s", response.Code, response.Body.String())
		}
	}
	yes := true
	if _, err := f.f.store.PatchLiteratureItem(t.Context(), owner, f.itemID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &yes}); err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-bibtex", map[string]any{"item_ids": []string{f.itemID}}, owner)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "bibtex") {
		t.Fatalf("Trash export HTTP%d %s", response.Code, response.Body.String())
	}
	oversized := make([]string, 201)
	for index := range oversized {
		oversized[index] = fmt.Sprintf("explicit-%d", index)
	}
	for _, body := range []any{map[string]any{"item_ids": oversized}, map[string]any{"item_ids": []string{}}, map[string]any{"item_ids": []string{f.itemID}, "owner": owner}, map[string]any{"item_ids": []string{f.itemID, f.itemID}}} {
		response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-bibtex", body, owner)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid envelope HTTP%d %s", response.Code, response.Body.String())
		}
	}
}
