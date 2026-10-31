package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// Exercise only the established PATCH/GET handlers and immutable operation
// ledger. The new read-only projection is asserted as JSON, not a new Go API.
func TestLiteratureHTTPRatingProjectionAndOriginalFieldWriter(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	seeded := seedLiteratureMetadataPatch(t, f)
	read := func(owner string) map[string]any {
		t.Helper()
		response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items/"+f.itemID, nil, owner)
		if response.Code != http.StatusOK {
			t.Fatalf("rating GET: HTTP%d %s", response.Code, response.Body.String())
		}
		var item map[string]any
		decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
		decoder.UseNumber()
		if decoder.Decode(&item) != nil {
			t.Fatal("invalid item JSON")
		}
		return item
	}
	if !reflect.DeepEqual(read(f.f.stream.OwnerID)["rating_view"], map[string]any{"known": true, "value": json.Number("0")}) {
		t.Fatal("live read did not project an unrated item")
	}
	command := map[string]any{"operation_id": f.input.OperationID, "expected_revision": seeded.Item.MetadataRevision, "metadata_patch": map[string]any{"set": map[string]any{"rating": 5}}}
	response := f.request(t, command)
	ack := literatureHTTPNumberReceipt(t, response)
	if !reflect.DeepEqual(read(f.f.stream.OwnerID)["rating_view"], map[string]any{"known": true, "value": json.Number("5")}) {
		t.Fatal("PATCH did not read back the real rating")
	}
	delete(ack.Item.Metadata, "rating")
	if !bytes.Equal(literatureHTTPNumberJSON(t, ack.Item.Metadata), literatureHTTPNumberJSON(t, seeded.Item.Metadata)) {
		t.Fatal("rating write replaced unedited opaque metadata")
	}
	clear := map[string]any{"operation_id": uuid.NewString(), "expected_revision": seeded.Item.MetadataRevision + 1, "metadata_patch": map[string]any{"set": map[string]any{"rating": 0}}}
	if got := f.request(t, clear); got.Code != http.StatusOK {
		t.Fatalf("clear rating HTTP%d", got.Code)
	}
	replay := f.request(t, command)
	if replay.Code != http.StatusOK || !bytes.Equal(literatureHTTPNumberCanonical(t, replay.Body.Bytes()), literatureHTTPNumberCanonical(t, response.Body.Bytes())) {
		t.Fatal("rating replay changed its original ACK")
	}
	if !reflect.DeepEqual(read(f.f.stream.OwnerID)["rating_view"], map[string]any{"known": true, "value": json.Number("0")}) {
		t.Fatal("old ACK overwrote the current zero rating")
	}
	command["operation_id"] = uuid.NewString()
	if got := f.request(t, command); got.Code != http.StatusConflict {
		t.Fatalf("rating ignored original CAS: HTTP%d %s", got.Code, got.Body.String())
	}
	if got := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items/"+f.itemID, nil, "another-owner"); got.Code != http.StatusNotFound {
		t.Fatalf("rating read crossed owner: HTTP%d", got.Code)
	}
	for _, extra := range []string{"rating_view", "rating"} {
		forged := map[string]any{"operation_id": uuid.NewString(), "expected_revision": seeded.Item.MetadataRevision + 2, "metadata_patch": map[string]any{"set": map[string]any{"rating": 4}}, extra: map[string]any{"known": true, "value": 4}}
		if got := f.request(t, forged); got.Code != http.StatusBadRequest {
			t.Fatalf("read-only %s became a second writer: HTTP%d", extra, got.Code)
		}
	}
}
