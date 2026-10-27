package literaturediscovery

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func publicationSnapshotFixture() PublicationSnapshot {
	return PublicationSnapshot{
		DOI:       "10.1234/source",
		CheckedAt: time.Date(2026, 10, 27, 1, 2, 3, 456, time.UTC),
		Notices:   []PublicationNotice{{DOI: "10.1234/notice", Type: "correction", Source: "crossref", Date: "2024-02-29"}},
	}
}

func publicationSnapshotObject() map[string]any {
	return map[string]any{
		"doi":        "10.1234/source",
		"checked_at": "2026-10-27T01:02:03.000000456Z",
		"notices":    []any{map[string]any{"doi": "10.1234/notice", "type": "correction", "source": "crossref", "date": "2024-02-29"}},
	}
}

func TestParsePublicationSnapshotRejectsInvalidPersistedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"doi-absent", func(object map[string]any) { delete(object, "doi") }},
		{"doi-null", func(object map[string]any) { object["doi"] = nil }},
		{"doi-noncanonical", func(object map[string]any) { object["doi"] = "10.1234/SOURCE" }},
		{"checked-at-absent", func(object map[string]any) { delete(object, "checked_at") }},
		{"checked-at-null", func(object map[string]any) { object["checked_at"] = nil }},
		{"checked-at-number", func(object map[string]any) { object["checked_at"] = 1 }},
		{"checked-at-offset", func(object map[string]any) { object["checked_at"] = "2026-10-27T02:02:03+01:00" }},
		{"checked-at-zero-time", func(object map[string]any) { object["checked_at"] = "0001-01-01T00:00:00Z" }},
		{"checked-at-invalid-date", func(object map[string]any) { object["checked_at"] = "2026-02-29T01:02:03Z" }},
		{"checked-at-nonstandard-fraction", func(object map[string]any) { object["checked_at"] = "2026-10-27T01:02:03,1Z" }},
		{"notices-absent", func(object map[string]any) { delete(object, "notices") }},
		{"notices-null", func(object map[string]any) { object["notices"] = nil }},
		{"notices-object", func(object map[string]any) { object["notices"] = map[string]any{} }},
		{"notice-null", func(object map[string]any) { object["notices"] = []any{nil} }},
		{"unknown-field", func(object map[string]any) { object["url"] = "https://example.test" }},
		{"field-case", func(object map[string]any) { object["DOI"] = object["doi"]; delete(object, "doi") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := publicationSnapshotObject()
			tc.change(object)
			raw, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot, valid := ParsePublicationSnapshot(raw); valid || !reflect.DeepEqual(snapshot, PublicationSnapshot{}) {
				t.Fatalf("invalid stored snapshot was accepted: %#v valid%v", snapshot, valid)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"doi-absent", func(notice map[string]any) { delete(notice, "doi") }},
		{"doi-null", func(notice map[string]any) { notice["doi"] = nil }},
		{"doi-url", func(notice map[string]any) { notice["doi"] = "https://doi.org/10.1234/notice" }},
		{"type-absent", func(notice map[string]any) { delete(notice, "type") }},
		{"type-null", func(notice map[string]any) { notice["type"] = nil }},
		{"type-empty", func(notice map[string]any) { notice["type"] = "  " }},
		{"type-noncanonical", func(notice map[string]any) { notice["type"] = " correction " }},
		{"type-too-long", func(notice map[string]any) { notice["type"] = strings.Repeat("a", 101) }},
		{"source-absent", func(notice map[string]any) { delete(notice, "source") }},
		{"source-null", func(notice map[string]any) { notice["source"] = nil }},
		{"source-invalid", func(notice map[string]any) { notice["source"] = "untrusted" }},
		{"date-null", func(notice map[string]any) { notice["date"] = nil }},
		{"date-empty", func(notice map[string]any) { notice["date"] = "" }},
		{"date-partial", func(notice map[string]any) { notice["date"] = "2024-02" }},
		{"date-invalid", func(notice map[string]any) { notice["date"] = "2023-02-29" }},
		{"unknown-field", func(notice map[string]any) { notice["url"] = "https://example.test" }},
	} {
		t.Run("notice-"+tc.name, func(t *testing.T) {
			object := publicationSnapshotObject()
			tc.change(object["notices"].([]any)[0].(map[string]any))
			raw, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot, valid := ParsePublicationSnapshot(raw); valid || !reflect.DeepEqual(snapshot, PublicationSnapshot{}) {
				t.Fatalf("invalid stored notice was accepted: %#v valid%v", snapshot, valid)
			}
		})
	}
}

func TestParsePublicationSnapshotRequiresOneCompleteJSONValue(t *testing.T) {
	raw, err := json.Marshal(publicationSnapshotFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{
		[]byte(`null`),
		[]byte(`[]`),
		[]byte(`{}`),
		raw[:len(raw)-1],
		append(append([]byte{}, raw...), []byte(` {}`)...),
		[]byte(strings.Repeat(" ", publicationMaxBytes+1)),
		[]byte{'{', '"', 0xff, '"', ':', '1', '}'},
	} {
		if snapshot, valid := ParsePublicationSnapshot(input); valid || !reflect.DeepEqual(snapshot, PublicationSnapshot{}) {
			t.Fatalf("non-closed or invalid JSON accepted: %#v valid%v", snapshot, valid)
		}
	}
}

func TestPublicationSnapshotRoundTripPreservesRecordsAndOptionalDate(t *testing.T) {
	for _, source := range []string{"publisher", "retraction-watch", "crossref"} {
		snapshot := publicationSnapshotFixture()
		snapshot.Notices[0].Source = source
		snapshot.Notices = append(snapshot.Notices, snapshot.Notices[0])
		snapshot.Notices[1].Date = ""
		raw, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		parsed, valid := ParsePublicationSnapshot(raw)
		if !valid || !reflect.DeepEqual(parsed, snapshot) {
			t.Fatalf("valid evidence changed on readback: %#v valid%v; want %#v", parsed, valid, snapshot)
		}
	}
	for _, count := range []int{0, 100, 101} {
		snapshot := publicationSnapshotFixture()
		notice := snapshot.Notices[0]
		snapshot.Notices = make([]PublicationNotice, count)
		for index := range snapshot.Notices {
			snapshot.Notices[index] = notice
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		parsed, valid := ParsePublicationSnapshot(raw)
		if valid != (count <= 100) || (valid && len(parsed.Notices) != count) {
			t.Fatalf("wrong notice boundary %d: %#v valid%v", count, parsed, valid)
		}
	}
	beforeEpoch := publicationSnapshotFixture()
	beforeEpoch.CheckedAt = time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC)
	raw, err := json.Marshal(beforeEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, valid := ParsePublicationSnapshot(raw); !valid || !reflect.DeepEqual(parsed, beforeEpoch) {
		t.Fatalf("legal UTC timestamp gained an unsupported epoch lower bound: %#v valid%v", parsed, valid)
	}
}

func TestFreezePublicationSnapshotValidatesAndDetachesEvidence(t *testing.T) {
	original := publicationSnapshotFixture()
	original.Notices = append(original.Notices, original.Notices[0])
	frozen, valid := FreezePublicationSnapshot(original)
	if !valid || frozen.CheckedAt.Location() != time.UTC || !reflect.DeepEqual(frozen, original) || len(frozen.Notices) != 2 {
		t.Fatalf("freeze changed provider semantics or original input: %#v %#v valid%v", original, frozen, valid)
	}
	original.Notices[0].DOI = "10.1234/changed"
	frozen.Notices[1].Source = "publisher"
	if frozen.Notices[0].DOI != "10.1234/notice" || original.Notices[1].Source != "crossref" {
		t.Fatal("frozen and caller-owned evidence share mutable records")
	}
	for _, tc := range []struct {
		name   string
		change func(*PublicationSnapshot)
	}{
		{"doi-noncanonical", func(snapshot *PublicationSnapshot) { snapshot.DOI = "10.1234/SOURCE" }},
		{"time-out-of-range", func(snapshot *PublicationSnapshot) { snapshot.CheckedAt = time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"time-zero", func(snapshot *PublicationSnapshot) { snapshot.CheckedAt = time.Time{} }},
		{"time-offset", func(snapshot *PublicationSnapshot) {
			snapshot.CheckedAt = snapshot.CheckedAt.In(time.FixedZone("offset", 3600))
		}},
		{"notices-nil", func(snapshot *PublicationSnapshot) { snapshot.Notices = nil }},
		{"notice-doi", func(snapshot *PublicationSnapshot) { snapshot.Notices[0].DOI = "invalid" }},
		{"notice-type-utf8", func(snapshot *PublicationSnapshot) { snapshot.Notices[0].Type = string([]byte{0xff}) }},
		{"notice-type-noncanonical", func(snapshot *PublicationSnapshot) { snapshot.Notices[0].Type = " correction " }},
		{"notice-source", func(snapshot *PublicationSnapshot) { snapshot.Notices[0].Source = "unknown" }},
		{"notice-date", func(snapshot *PublicationSnapshot) { snapshot.Notices[0].Date = "2023-02-29" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := publicationSnapshotFixture()
			tc.change(&snapshot)
			if frozen, valid := FreezePublicationSnapshot(snapshot); valid || !reflect.DeepEqual(frozen, PublicationSnapshot{}) {
				t.Fatalf("invalid native evidence was frozen: %#v valid%v", frozen, valid)
			}
		})
	}
}
