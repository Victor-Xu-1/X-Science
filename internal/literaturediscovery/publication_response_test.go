package literaturediscovery

import (
	"reflect"
	"testing"
)

func publicationResponseJSON(updatedBy string) []byte {
	field := ""
	if updatedBy != "" {
		field = `,"updated-by":` + updatedBy
	}
	return []byte(`{"status":"ok","message-type":"work","message":{"DOI":"10.1234/source"` + field + `}}`)
}

func publicationNoticeJSON(fields string) string {
	return `{"DOI":"10.1234/notice","type":"correction"` + fields + `}`
}

func TestPublicationNoticesRejectsPresentInvalidOptionalFields(t *testing.T) {
	notice := func(fields string) string { return "[" + publicationNoticeJSON(fields) + "]" }
	for _, tc := range []struct {
		name      string
		updatedBy string
	}{
		{"updates-null", `null`},
		{"updates-object", `{}`},
		{"updates-string", `""`},
		{"updates-number", `0`},
		{"updates-bool", `false`},
		{"update-null", `[null]`},
		{"source-null", notice(`,"source":null`)},
		{"source-empty", notice(`,"source":""`)},
		{"source-output-default", notice(`,"source":"crossref"`)},
		{"source-object", notice(`,"source":{}`)},
		{"source-array", notice(`,"source":[]`)},
		{"source-number", notice(`,"source":0`)},
		{"source-bool", notice(`,"source":false`)},
		{"updated-null", notice(`,"updated":null`)},
		{"updated-array", notice(`,"updated":[]`)},
		{"updated-string", notice(`,"updated":""`)},
		{"updated-number", notice(`,"updated":0`)},
		{"updated-bool", notice(`,"updated":false`)},
		{"date-parts-null", notice(`,"updated":{"date-parts":null}`)},
		{"date-parts-object", notice(`,"updated":{"date-parts":{}}`)},
		{"date-parts-string", notice(`,"updated":{"date-parts":""}`)},
		{"date-parts-number", notice(`,"updated":{"date-parts":0}`)},
		{"date-parts-bool", notice(`,"updated":{"date-parts":false}`)},
		{"date-array-null", notice(`,"updated":{"date-parts":[null]}`)},
		{"date-array-object", notice(`,"updated":{"date-parts":[{}]}`)},
		{"date-array-number", notice(`,"updated":{"date-parts":[2024]}`)},
		{"date-integer-null", notice(`,"updated":{"date-parts":[[2024,null,29]]}`)},
		{"date-integer-string", notice(`,"updated":{"date-parts":[["2024",2,29]]}`)},
		{"date-integer-fraction", notice(`,"updated":{"date-parts":[[2024,2.5,29]]}`)},
		{"date-integer-bool", notice(`,"updated":{"date-parts":[[2024,false,29]]}`)},
		{"date-integer-array", notice(`,"updated":{"date-parts":[[2024,[],29]]}`)},
		{"date-integer-object", notice(`,"updated":{"date-parts":[[2024,{},29]]}`)},
		{"later-date-array-null", notice(`,"updated":{"date-parts":[[2024,2,29],null]}`)},
		{"later-date-integer-null", notice(`,"updated":{"date-parts":[[2024,2,29],[null]]}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			notices, valid := publicationNotices(publicationResponseJSON(tc.updatedBy), "10.1234/source")
			if valid || notices != nil {
				t.Fatalf("invalid provider fields produced evidence: %#v valid%v", notices, valid)
			}
		})
	}
}

func TestPublicationNoticesPreservesOptionalFieldsRecordsAndPartialDates(t *testing.T) {
	notice := func(fields string) string { return "[" + publicationNoticeJSON(fields) + "]" }
	defaultNotice := PublicationNotice{DOI: "10.1234/notice", Type: "correction", Source: "crossref"}
	for _, tc := range []struct {
		name      string
		updatedBy string
		want      []PublicationNotice
	}{
		{"updates-absent", "", []PublicationNotice{}},
		{"updates-empty", `[]`, []PublicationNotice{}},
		{"optional-fields-absent", notice(""), []PublicationNotice{defaultNotice}},
		{"source-publisher", notice(`,"source":"publisher"`), []PublicationNotice{{DOI: "10.1234/notice", Type: "correction", Source: "publisher"}}},
		{"source-retraction-watch", notice(`,"source":"retraction-watch"`), []PublicationNotice{{DOI: "10.1234/notice", Type: "correction", Source: "retraction-watch"}}},
		{"updated-empty", notice(`,"updated":{}`), []PublicationNotice{defaultNotice}},
		{"date-parts-empty", notice(`,"updated":{"date-parts":[]}`), []PublicationNotice{defaultNotice}},
		{"date-array-empty", notice(`,"updated":{"date-parts":[[]]}`), []PublicationNotice{defaultNotice}},
		{"date-partial", notice(`,"updated":{"date-parts":[[2024,2]]}`), []PublicationNotice{defaultNotice}},
		{"date-extended", notice(`,"updated":{"date-parts":[[2024,2,29,0]]}`), []PublicationNotice{defaultNotice}},
		{"date-not-calendar", notice(`,"updated":{"date-parts":[[2023,2,29]]}`), []PublicationNotice{defaultNotice}},
		{"date-out-of-range", notice(`,"updated":{"date-parts":[[999,2,28]]}`), []PublicationNotice{defaultNotice}},
		{"date-first-only", notice(`,"updated":{"date-parts":[[],[2024,2,29]]}`), []PublicationNotice{defaultNotice}},
		{"date-complete", notice(`,"updated":{"date-parts":[[2024,2,29]]}`), []PublicationNotice{{DOI: "10.1234/notice", Type: "correction", Source: "crossref", Date: "2024-02-29"}}},
		{"duplicate-records", "[" + publicationNoticeJSON("") + "," + publicationNoticeJSON("") + "]", []PublicationNotice{defaultNotice, defaultNotice}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			notices, valid := publicationNotices(publicationResponseJSON(tc.updatedBy), "10.1234/source")
			if !valid || !reflect.DeepEqual(notices, tc.want) {
				t.Fatalf("provider records changed: %#v valid%v; want %#v", notices, valid, tc.want)
			}
		})
	}
}
