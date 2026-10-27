package workspace

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLiteratureReadingStatusPreservesLegacyItemWireAndRejectsInvalidStates(t *testing.T) {
	legacy := []byte(`{"id":"legacy","title":"Legacy reference","metadata_revision":1,"metadata":{"opaque":1e400},"deleted":false,"project_ids":[],"attachments":[]}`)
	var item LiteratureItem
	if err := json.Unmarshal(legacy, &item); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(item)
	if err != nil || !bytes.Equal(encoded, legacy) {
		t.Fatalf("legacy acknowledgement changed: %s %v", encoded, err)
	}
	for _, name := range []string{"reading_status", "Reading_Status"} {
		for _, invalid := range []string{`null`, `""`, `"archived"`, `1`, `{}`} {
			raw := strings.Replace(string(legacy), `"metadata_revision":1`, `"metadata_revision":1,"`+name+`":`+invalid, 1)
			if err := json.Unmarshal([]byte(raw), &item); err == nil {
				t.Fatalf("invalid explicit reading status accepted: %s=%s", name, invalid)
			}
		}
	}
	shadow := strings.Replace(string(legacy), `"metadata_revision":1`, `"metadata_revision":1,"reading_status":"read","Reading_Status":"archived"`, 1)
	if err := json.Unmarshal([]byte(shadow), &item); err == nil {
		t.Fatal("a noncanonical alias bypassed validation of the canonical field")
	}
	current := strings.Replace(string(legacy), `"metadata_revision":1`, `"metadata_revision":1,"reading_status":"read"`, 1)
	if err := json.Unmarshal([]byte(current), &item); err != nil || item.ReadingStatus != LiteratureRead {
		t.Fatalf("current stored state was not decoded: %+v %v", item, err)
	}
}
