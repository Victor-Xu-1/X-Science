package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
	workspace "x-science/internal/persistence/workspace"
)

type referenceExportWire struct {
	Schema  string   `json:"schema"`
	ItemIDs []string `json:"item_ids"`
	Format  string   `json:"format"`
	Content string   `json:"content"`
}

func TestLiteratureReferenceExportHTTPPreservesLegacyBytesAndFormats(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	item, err := f.f.store.CreateManualLiterature(t.Context(), owner, workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Preserved title", Metadata: map[string]any{"authors": []string{"Ada Lovelace"}, "year": json.Number("2024"), "doi": "10.1234/stable"}})
	if err != nil {
		t.Fatal(err)
	}
	id := item.Item.ID
	expected := fmt.Sprintf("@misc{x_%s,\n  title = {Preserved title},\n  author = {{Ada Lovelace}},\n  year = {2024},\n  doi = {10.1234/stable}\n}\n", hex.EncodeToString([]byte(id)))
	legacy := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-bibtex", map[string]any{"item_ids": []string{id}}, owner)
	encoded, _ := json.Marshal(expected)
	wire := fmt.Sprintf("{\"schema\":\"x-science.literature-bibtex.v1\",\"item_ids\":[%q],\"bibtex\":%s}\n", id, encoded)
	if legacy.Code != 200 || !bytes.Equal(legacy.Body.Bytes(), []byte(wire)) {
		t.Fatalf("cached17 wire changed HTTP%d %s", legacy.Code, legacy.Body.String())
	}
	for _, format := range []string{"bibtex", "ris"} {
		response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": []string{id}, "format": format}, owner)
		var result referenceExportWire
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Schema != "x-science.literature-export.v1" || result.Format != format || len(result.ItemIDs) != 1 || result.ItemIDs[0] != id {
			t.Fatalf("generic export HTTP%d %s", response.Code, response.Body.String())
		}
		if format == "bibtex" && result.Content != expected {
			t.Fatal("generic BibTeX did not share the old canonical serialization")
		}
		if format == "ris" && result.Content != "TY  - JOUR\nAU  - Ada Lovelace\nTI  - Preserved title\nPY  - 2024///\nDO  - 10.1234/stable\nKW  - one\nKW  - two\nER  - \n" {
			t.Fatalf("RIS actual year/literal author wire: %q", result.Content)
		}
	}
}
func TestLiteratureReferenceExportHTTPRISLiteralTagsAndUnavailableSelection(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	item, err := f.f.store.CreateManualLiterature(t.Context(), owner, workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: "研究\r\nER  - \nTY  - BOOK\u2028tail", Metadata: map[string]any{"item_type": "book", "authors": []string{"Literal\tName", "研究\u00a0组"}, "year": "2024", "pages": "10–12-14", "container_title": "Collected works", "keywords": []string{"alpha, beta", "gamma"}, "editors": []string{"Editor, Ada"}, "translators": []string{"Translator"}, "edition": "2", "container_title_short": "Short", "pmid": "123"}})
	if err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": []string{item.Item.ID}, "format": "ris"}, owner)
	var result referenceExportWire
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("RIS HTTP%d %s", response.Code, response.Body.String())
	}
	want := "TY  - BOOK\nAU  - Literal Name\nAU  - 研究\u00a0组\nTI  - 研究 ER - TY - BOOK tail\nT2  - Collected works\nPY  - 2024///\nSP  - 10\nEP  - 12-14\nKW  - alpha, beta\nKW  - gamma\nN1  - PMID: 123\nA3  - Editor, Ada\nA4  - Translator\nET  - 2\nJ2  - Short\nER  - \n"
	if result.Content != want {
		t.Fatalf("literal RIS record/tag wire differs:\n%q\nwant\n%q", result.Content, want)
	}
	if strings.Count(result.Content, "\nTY  - ") != 0 || strings.Count(result.Content, "\nER  - ") != 1 || strings.Contains(result.Content, "DA  -") {
		t.Fatal("value injected a record or a date was invented")
	}

	preprint, err := f.f.store.CreateManualLiterature(t.Context(), owner, workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Preprint", Metadata: map[string]any{"item_type": "preprint", "container_title": "Repository"}})
	if err != nil {
		t.Fatal(err)
	}
	preResponse := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": []string{preprint.Item.ID}, "format": "ris"}, owner)
	var preWire referenceExportWire
	if preResponse.Code != 200 || json.Unmarshal(preResponse.Body.Bytes(), &preWire) != nil || !strings.Contains(preWire.Content, "TY  - JOUR\n") || !strings.Contains(preWire.Content, "T2  - Repository\n") || strings.Contains(preWire.Content, "JO  -") {
		t.Fatalf("fallback type invented journal container: %s", preResponse.Body.String())
	}
	foreign, err := f.f.store.CreateManualLiterature(t.Context(), uuid.NewString(), workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Foreign"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{uuid.NewString(), foreign.Item.ID} {
		response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": []string{item.Item.ID, bad}, "format": "ris"}, owner)
		if response.Code != 404 || strings.Contains(response.Body.String(), "content") {
			t.Fatalf("partial/unowned RIS HTTP%d %s", response.Code, response.Body.String())
		}
	}
	yes := true
	if _, err = f.f.store.PatchLiteratureItem(t.Context(), owner, item.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &yes}); err != nil {
		t.Fatal(err)
	}
	response = p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": []string{item.Item.ID}, "format": "ris"}, owner)
	if response.Code != 404 {
		t.Fatalf("Trash RIS export HTTP%d", response.Code)
	}
	for _, format := range []string{"", "pdf", "RIS"} {
		response = p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": []string{f.itemID}, "format": format}, owner)
		if response.Code != 400 {
			t.Fatalf("unknown export format HTTP%d", response.Code)
		}
	}
	oversized := make([]string, 201)
	for i := range oversized {
		oversized[i] = fmt.Sprintf("item-%d", i)
	}
	response = p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": oversized, "format": "ris"}, owner)
	if response.Code != 400 {
		t.Fatalf("unbounded export HTTP%d", response.Code)
	}
	longIDs := make([]string, 200)
	for i := range longIDs {
		longIDs[i] = strings.Repeat("x", 509) + fmt.Sprintf("%03d", i)
	}
	response = p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": longIDs, "format": "ris"}, owner)
	if response.Code != 400 {
		t.Fatalf("oversized UTF8 envelope HTTP%d", response.Code)
	}
	response = p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": []string{f.itemID}, "format": "ris", "owner": owner}, owner)
	if response.Code != 400 {
		t.Fatalf("unknown export request field HTTP%d", response.Code)
	}

}
