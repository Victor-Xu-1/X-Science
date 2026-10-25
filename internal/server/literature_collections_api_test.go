package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func collectionHTTPReceipt(t *testing.T, response *httptest.ResponseRecorder, action string) workspace.LiteratureCollectionReceipt {
	t.Helper()
	var receipt workspace.LiteratureCollectionReceipt
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &receipt) != nil ||
		receipt.Schema != "x-science.literature-collection-receipt.v1" || receipt.Action != action || receipt.CollectionID == "" {
		t.Fatalf("collection %s: HTTP%d %s", action, response.Code, response.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil || raw["item"] != nil {
		t.Fatal("collection acknowledgement fabricated an item receipt")
	}
	if action == "create" || action == "update" {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw["collection"], &item) != nil || item["parent_id"] == nil || item["description"] == nil {
			t.Fatal("collection wire omitted the nullable parent or description required by the Web decoder")
		}
		var description string
		if json.Unmarshal(item["description"], &description) != nil {
			t.Fatal("collection description did not use the shared string contract")
		}
	}
	return receipt
}

func TestLiteratureCollectionHTTPPersistsFiltersAndReplaysOriginalAcknowledgement(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		return p3JSONRequest(t, f.f.server, method, path, body, owner)
	}
	createBody := map[string]any{"operation_id": uuid.NewString(), "name": "  Ｒesearch  "}
	first := request(http.MethodPost, "/api/literature/collections", createBody)
	created := collectionHTTPReceipt(t, first, "create")
	if created.Collection.Name != "Research" {
		t.Fatal("create did not acknowledge the reference-normalized name")
	}
	path := "/api/literature/collections/" + created.CollectionID
	if replay := request(http.MethodPost, "/api/literature/collections", createBody); replay.Code != 200 || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) {
		t.Fatal("the original create request did not replay its exact acknowledged result")
	}
	changed := map[string]any{"operation_id": createBody["operation_id"], "name": "Different request"}
	if response := request(http.MethodPost, "/api/literature/collections", changed); response.Code != http.StatusConflict {
		t.Fatalf("changed payload reused a committed operation: HTTP%d", response.Code)
	}
	member := request(http.MethodPut, path+"/items/"+f.itemID, map[string]any{"operation_id": uuid.NewString(), "included": true})
	joined := collectionHTTPReceipt(t, member, "membership")
	if joined.ItemID != f.itemID {
		t.Fatal("membership acknowledgement lost the exact item coordinate")
	}
	tree := request(http.MethodGet, "/api/literature/collections", nil)
	var treeWire struct {
		Schema      string `json:"schema"`
		Collections []struct {
			ID    string `json:"id"`
			Count int    `json:"item_count"`
		} `json:"collections"`
	}
	if tree.Code != 200 || json.Unmarshal(tree.Body.Bytes(), &treeWire) != nil || treeWire.Schema != "x-science.literature-collections.v1" ||
		len(treeWire.Collections) != 1 || treeWire.Collections[0].ID != created.CollectionID || treeWire.Collections[0].Count != 1 {
		t.Fatalf("owned collection directory did not acknowledge the visible member count: HTTP%d %s", tree.Code, tree.Body.String())
	}
	read := request(http.MethodGet, "/api/literature/items/"+f.itemID+"/collections", nil)
	var membership workspace.LiteratureCollectionMembership
	if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &membership) != nil ||
		membership.ItemID != f.itemID || len(membership.CollectionIDs) != 1 || membership.CollectionIDs[0] != created.CollectionID {
		t.Fatalf("membership did not read back: HTTP%d %s", read.Code, read.Body.String())
	}
	filtered := request(http.MethodGet, "/api/literature/items?collection_id="+created.CollectionID, nil)
	var page workspace.LiteraturePage
	if filtered.Code != 200 || json.Unmarshal(filtered.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != f.itemID {
		t.Fatalf("collection did not filter the actual catalogue: HTTP%d %s", filtered.Code, filtered.Body.String())
	}
	updated := request(http.MethodPatch, path, map[string]any{
		"operation_id": uuid.NewString(), "expected_revision": 1, "name": "Edited collection", "description": "Methods",
	})
	collectionHTTPReceipt(t, updated, "update")
	stale := request(http.MethodPatch, path, map[string]any{"operation_id": uuid.NewString(), "expected_revision": 1, "name": "Lost edit"})
	if stale.Code != http.StatusConflict || !bytes.Contains(stale.Body.Bytes(), []byte("LITERATURE_CONFLICT")) {
		t.Fatal("stale metadata revision was not returned as a real conflict")
	}
	duplicate := request(http.MethodPost, "/api/literature/collections", map[string]any{
		"operation_id": uuid.NewString(), "name": "ＥＤＩＴＥＤ\tCOLLECTION",
	})
	if duplicate.Code != http.StatusConflict || !bytes.Contains(duplicate.Body.Bytes(), []byte("LITERATURE_COLLECTION_NAME_CONFLICT")) {
		t.Fatal("normalized sibling collision did not retain its distinct error")
	}
	collectionHTTPReceipt(t, request(http.MethodDelete, path, map[string]any{"operation_id": uuid.NewString()}), "delete")
	if detail := request(http.MethodGet, "/api/literature/items/"+f.itemID, nil); detail.Code != http.StatusOK {
		t.Fatal("deleting a collection removed its paper or attachment")
	}
	if replay := request(http.MethodPost, "/api/literature/collections", createBody); replay.Code != 200 || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) {
		t.Fatal("later deletion prevented the original immutable acknowledgement from resolving")
	}
	listed := request(http.MethodGet, "/api/literature/collections", nil)
	var collections workspace.LiteratureCollectionPage
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &collections) != nil || len(collections.Collections) != 0 {
		t.Fatal("an original-request retry recreated a deleted collection")
	}
}

func TestLiteratureCollectionHTTPFencesOwnersAndStrictMutationEnvelopes(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	created := collectionHTTPReceipt(t, p3JSONRequest(t, f.f.server, http.MethodPost, "/api/literature/collections",
		map[string]any{"operation_id": uuid.NewString(), "name": "Owned"}, owner), "create")
	path := "/api/literature/collections/" + created.CollectionID
	for _, test := range []struct {
		name, method, path, owner string
		body                      any
		status                    int
	}{
		{"foreign filter", http.MethodGet, "/api/literature/items?collection_id=" + created.CollectionID, "foreign-owner", nil, 404},
		{"foreign member read", http.MethodGet, "/api/literature/items/" + f.itemID + "/collections", "foreign-owner", nil, 404},
		{"foreign delete", http.MethodDelete, path, "foreign-owner", map[string]any{"operation_id": uuid.NewString()}, 404},
		{"missing inclusion", http.MethodPut, path + "/items/" + f.itemID, owner, map[string]any{"operation_id": uuid.NewString()}, 400},
		{"invalid operation", http.MethodDelete, path, owner, map[string]any{"operation_id": "not-a-uuid"}, 400},
		{"unknown coordinate", http.MethodDelete, path, owner, map[string]any{"operation_id": uuid.NewString(), "collection_id": "other"}, 400},
		{"unbound query", http.MethodPost, "/api/literature/collections?unexpected=1", owner, map[string]any{"operation_id": uuid.NewString(), "name": "Other"}, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := p3JSONRequest(t, f.f.server, test.method, test.path, test.body, test.owner)
			if response.Code != test.status {
				t.Fatalf("HTTP%d want%d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}
