package server

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"testing"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureRestorePreviewHTTPUsesOriginalDeletedRevision(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	deleted := true
	receipt, err := f.f.store.PatchLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted})
	if err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?all_item_ids=true&lifecycle=deleted&restore_preview=true", nil, f.f.stream.OwnerID)
	var preview struct {
		Schema string `json:"schema"`
		Items  []struct {
			ID       string `json:"id"`
			Revision int64  `json:"metadata_revision"`
		} `json:"items"`
		Total int64 `json:"total_count"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &preview) != nil || preview.Schema != "x-science.literature-restore-preview.v1" || preview.Total != 1 || len(preview.Items) != 1 || preview.Items[0].ID != f.itemID || preview.Items[0].Revision != receipt.Item.MetadataRevision {
		t.Fatalf("complete original restore preview rejected or changed: HTTP%d %s", response.Code, response.Body.String())
	}
}

func TestLiteratureRestorePreviewHTTPRejectsUndefinedCombinations(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	for _, query := range []string{
		"restore_preview=true", "all_item_ids=true&restore_preview=true", "all_item_ids=true&lifecycle=active&restore_preview=true", "all_item_ids=false&lifecycle=deleted&restore_preview=true", "all_item_ids=true&lifecycle=deleted&restore_preview=false", "all_item_ids=true&lifecycle=deleted&restore_preview=true&limit=1", "all_item_ids=true&lifecycle=deleted&restore_preview=true&cursor=x", "all_item_ids=true&lifecycle=deleted&restore_preview=true&restore_preview=true", "all_item_ids=true&lifecycle=deleted&restore_preview=true&sort=title-asc",
	} {
		t.Run(query, func(t *testing.T) {
			response := p3JSONRequest(t, f.f.server, "GET", "/api/literature/items?"+query, nil, f.f.stream.OwnerID)
			if response.Code != 400 {
				t.Fatalf("undefined combination accepted: HTTP%d %s", response.Code, response.Body.String())
			}
		})
	}
}
