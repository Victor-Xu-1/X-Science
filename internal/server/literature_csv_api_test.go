package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureCSVHTTPOrderedMetadataAndWholeBatchFailure(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	create := func(user, title string) workspace.LiteratureReceipt {
		t.Helper()
		item, err := f.f.store.CreateManualLiterature(t.Context(), user, workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: title, Metadata: map[string]any{"authors": []string{"Literal, Ada", "研究组"}, "volume": json.Number("900719925474099312345"), "personal_note": "private-note"}})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	first, second := create(owner, "First"), create(owner, "研究, \"quoted\"\nsecond")
	ids := []string{second.Item.ID, first.Item.ID}
	response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": ids, "format": "csv"}, owner)
	var result referenceExportWire
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Format != "csv" || result.Schema != "x-science.literature-export.v1" || !reflect.DeepEqual(result.ItemIDs, ids) {
		t.Fatalf("CSV envelope HTTP%d %s", response.Code, response.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(result.Content)).ReadAll()
	columns := []string{"item_id", "title", "authors", "year", "item_type", "publication", "volume", "issue", "pages", "doi", "pmid", "pmcid", "arxiv_id", "url", "abstract", "language"}
	if err != nil || len(rows) != 3 || !reflect.DeepEqual(rows[0], columns) || rows[1][0] != ids[0] || rows[2][0] != ids[1] || rows[1][2] != "Literal, Ada; 研究组" || rows[1][6] != "900719925474099312345" {
		t.Fatalf("CSV metadata/order rows=%q err=%v", rows, err)
	}
	if strings.HasPrefix(result.Content, "\ufeff") || !strings.HasSuffix(result.Content, "\r\n") || strings.Contains(result.Content, "private-note") {
		t.Fatalf("CSV file boundary/private data contract: %q", result.Content)
	}
	foreign := create(uuid.NewString(), "Foreign")
	deleted := true
	if _, err := f.f.store.PatchLiteratureItem(t.Context(), owner, first.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted}); err != nil {
		t.Fatal(err)
	}
	for _, unavailable := range []string{uuid.NewString(), foreign.Item.ID, first.Item.ID} {
		response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": []string{second.Item.ID, unavailable}, "format": "csv"}, owner)
		if response.Code != 404 || strings.Contains(response.Body.String(), "content") {
			t.Fatalf("partial/unowned/deleted CSV HTTP%d %s", response.Code, response.Body.String())
		}
	}
	oversized := make([]string, 201)
	for index := range oversized {
		oversized[index] = fmt.Sprintf("item-%d", index)
	}
	for _, ids := range [][]string{nil, {second.Item.ID, second.Item.ID}, oversized} {
		response := p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/export-references", map[string]any{"item_ids": ids, "format": "csv"}, owner)
		if response.Code != 400 {
			t.Fatalf("invalid CSV selection HTTP%d %s", response.Code, response.Body.String())
		}
	}
}
