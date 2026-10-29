package server

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func lifecyclePage(t *testing.T, response *httptest.ResponseRecorder) workspace.LiteraturePage {
	t.Helper()
	var page workspace.LiteraturePage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatalf("page failed HTTP%d %s", response.Code, response.Body.String())
	}
	return page
}

func TestLiteratureTrashHTTPListsOriginalSoftDeletedItem(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	deleted := true
	receipt, err := f.f.store.PatchLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted})
	if err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?lifecycle=deleted", nil, f.f.stream.OwnerID)
	var page workspace.LiteraturePage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != receipt.Item.ID || !page.Items[0].Deleted || page.TotalCount == nil || *page.TotalCount != 1 {
		t.Fatalf("Trash query did not expose original soft-deleted catalogue item: HTTP%d %s", response.Code, response.Body.String())
	}
}

func TestLiteratureLifecycleHTTPReplaysAndRestoresOriginalLinksAndContent(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	ctx, owner := t.Context(), f.f.stream.OwnerID
	if _, err := f.f.store.SetLiteratureProject(ctx, owner, f.itemID, f.f.stream.ProjectID, uuid.NewString(), true); err != nil {
		t.Fatal(err)
	}
	collection, err := f.f.store.CreateLiteratureCollection(ctx, owner, workspace.LiteratureCollectionCreate{OperationID: uuid.NewString(), Name: "Lifecycle collection"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.f.store.SetLiteratureCollectionItem(ctx, owner, collection.CollectionID, f.itemID, uuid.NewString(), true); err != nil {
		t.Fatal(err)
	}
	before, err := f.f.store.GetLiteratureItem(ctx, owner, f.itemID)
	if err != nil {
		t.Fatal(err)
	}
	contentPath := "/api/literature/attachment-versions/" + before.Attachments[0].Versions[0].ID + "/content"
	if response := p3JSONRequest(t, f.f.server, "GET", contentPath, nil, owner); response.Code != 200 {
		t.Fatalf("active content unavailable: %d", response.Code)
	}
	path := "/api/literature/items/" + f.itemID
	body := map[string]any{"operation_id": uuid.NewString(), "expected_revision": before.MetadataRevision, "deleted": true}
	first := p3JSONRequest(t, f.f.server, "PATCH", path, body, owner)
	var deleted workspace.LiteratureReceipt
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &deleted) != nil || !deleted.Item.Deleted {
		t.Fatalf("delete failed: %s", first.Body.String())
	}
	if replay := p3JSONRequest(t, f.f.server, "PATCH", path, body, owner); replay.Code != 200 || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatal("lost-response retry did not replay exact operation ACK")
	}
	if response := p3JSONRequest(t, f.f.server, "GET", path, nil, owner); response.Code != 404 {
		t.Fatal("deleted detail guard changed")
	}
	if response := p3JSONRequest(t, f.f.server, "GET", contentPath, nil, owner); response.Code != 404 {
		t.Fatal("deleted preview guard changed")
	}
	stale := map[string]any{"operation_id": uuid.NewString(), "expected_revision": before.MetadataRevision, "deleted": false}
	if response := p3JSONRequest(t, f.f.server, "PATCH", path, stale, owner); response.Code != 409 {
		t.Fatal("stale restore was accepted")
	}
	body["deleted"] = false
	if response := p3JSONRequest(t, f.f.server, "PATCH", path, body, owner); response.Code != 409 {
		t.Fatal("committed operation changed payload")
	}
	restore := map[string]any{"operation_id": uuid.NewString(), "expected_revision": deleted.Item.MetadataRevision, "deleted": false}
	if response := p3JSONRequest(t, f.f.server, "PATCH", path, restore, owner); response.Code != 200 {
		t.Fatalf("restore failed: %s", response.Body.String())
	}
	after, err := f.f.store.GetLiteratureItem(ctx, owner, f.itemID)
	if err != nil || after.Deleted || after.MetadataRevision != before.MetadataRevision+2 || !reflect.DeepEqual(before.Metadata, after.Metadata) || !reflect.DeepEqual(before.ProjectIDs, after.ProjectIDs) || !reflect.DeepEqual(before.Attachments, after.Attachments) {
		t.Fatalf("restore changed source metadata/links/version: %v", err)
	}
	members, err := f.f.store.GetLiteratureItemCollections(ctx, owner, f.itemID)
	if err != nil || !reflect.DeepEqual(members.CollectionIDs, []string{collection.CollectionID}) {
		t.Fatal("restore lost original collection")
	}
	if response := p3JSONRequest(t, f.f.server, "GET", contentPath, nil, owner); response.Code != 200 {
		t.Fatal("restored content not readable")
	}
}

func TestLiteratureLifecycleHTTPBindsCursorScopeAndPreservesActiveBytes(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	ctx, owner := t.Context(), f.f.stream.OwnerID
	ids := []string{"lifecycle-a", "lifecycle-b", "lifecycle-c", "lifecycle-d"}
	if err := f.f.store.WithTransaction(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,reading_status,created_at,updated_at) VALUES(?,?,'Lifecycle matching','{}','read','2026-01-01','2026-01-01')`, id, owner); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	collection, err := f.f.store.CreateLiteratureCollection(ctx, owner, workspace.LiteratureCollectionCreate{OperationID: uuid.NewString(), Name: "Cursor collection"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err = f.f.store.SetLiteratureProject(ctx, owner, id, f.f.stream.ProjectID, uuid.NewString(), true); err != nil {
			t.Fatal(err)
		}
		if _, err = f.f.store.SetLiteratureCollectionItem(ctx, owner, collection.CollectionID, id, uuid.NewString(), true); err != nil {
			t.Fatal(err)
		}
	}
	deleted := true
	for _, id := range ids[1:3] {
		if _, err = f.f.store.PatchLiteratureItem(ctx, owner, id, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted}); err != nil {
			t.Fatal(err)
		}
	}
	active := lifecyclePage(t, p3JSONRequest(t, f.f.server, "GET", "/api/literature/items?query=Lifecycle&limit=1", nil, owner))
	legacy, _ := json.Marshal(struct{ After, Query, Project, Owner string }{ids[0], "Lifecycle", "", owner})
	if active.NextCursor != base64.RawURLEncoding.EncodeToString(legacy) {
		t.Fatal("default active cursor bytes changed")
	}
	explicit := lifecyclePage(t, p3JSONRequest(t, f.f.server, "GET", "/api/literature/items?query=Lifecycle&limit=1&lifecycle=active", nil, owner))
	if explicit.NextCursor != active.NextCursor {
		t.Fatal("explicit active changed legacy cursor")
	}
	query := url.Values{"query": {"Lifecycle"}, "project_id": {f.f.stream.ProjectID}, "collection_id": {collection.CollectionID}, "reading_status": {"read"}, "lifecycle": {"deleted"}, "limit": {"1"}}
	get := func(values url.Values, asOwner string) *httptest.ResponseRecorder {
		return p3JSONRequest(t, f.f.server, "GET", "/api/literature/items?"+values.Encode(), nil, asOwner)
	}
	first := lifecyclePage(t, get(query, owner))
	if len(first.Items) != 1 || first.Items[0].ID != ids[1] || first.TotalCount == nil || *first.TotalCount != 2 || first.NextCursor == "" {
		t.Fatal("deleted filtered page/count mismatch")
	}
	if _, err := base64.RawURLEncoding.DecodeString(first.NextCursor); err == nil || !strings.Contains(first.NextCursor, ".") {
		t.Fatal("old active decoder could accept deleted cursor")
	}
	query.Set("cursor", first.NextCursor)
	second := lifecyclePage(t, get(query, owner))
	if len(second.Items) != 1 || second.Items[0].ID != ids[2] || second.NextCursor != "" {
		t.Fatal("deleted next page mismatched")
	}
	for key, value := range map[string]string{"query": "Other", "project_id": "other-project", "collection_id": "", "reading_status": "unread", "lifecycle": "active"} {
		copy, _ := url.ParseQuery(query.Encode())
		copy.Set(key, value)
		if response := get(copy, owner); response.Code != 400 {
			t.Fatalf("cross-%s cursor accepted: HTTP%d", key, response.Code)
		}
	}
	if response := get(query, "unrelated-owner"); response.Code != 400 {
		t.Fatal("cursor owner changed")
	}
	query.Set("cursor", active.NextCursor)
	if response := get(query, owner); response.Code != 400 {
		t.Fatal("active cursor accepted in deleted scope")
	}
	query.Del("cursor")
	query.Del("limit")
	query.Set("all_item_ids", "true")
	var selection workspace.LiteratureSelection
	response := get(query, owner)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &selection) != nil || selection.TotalCount != 2 || !reflect.DeepEqual(selection.ItemIDs, ids[1:3]) {
		t.Fatal("deleted complete-ID scope diverged from page/count")
	}
	for _, raw := range []string{"lifecycle=", "lifecycle=all", "lifecycle=active&lifecycle=deleted", "all_item_ids=true&lifecycle=unknown"} {
		if response := p3JSONRequest(t, f.f.server, "GET", "/api/literature/items?"+raw, nil, owner); response.Code != 400 {
			t.Fatalf("invalid lifecycle accepted: %s", raw)
		}
	}
}
