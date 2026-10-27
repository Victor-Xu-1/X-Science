package literaturediscovery

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var publicationTimestampPattern = regexp.MustCompile(`^[1-9][0-9]{3}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)

func publicationObject(raw []byte, required []string, optional ...string) (map[string]json.RawMessage, bool) {
	if len(raw) > publicationMaxBytes || !utf8.Valid(raw) {
		return nil, false
	}
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&object) != nil || object == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, false
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		if _, present := object[key]; !present {
			return nil, false
		}
		allowed[key] = true
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range object {
		if !allowed[key] {
			return nil, false
		}
	}
	return object, true
}

// ParsePublicationSnapshot accepts the persisted evidence schema, not provider
// JSON. Required fields and explicit null values must never become zero values.
func ParsePublicationSnapshot(raw []byte) (PublicationSnapshot, bool) {
	var empty PublicationSnapshot
	object, valid := publicationObject(raw, []string{"doi", "checked_at", "notices"})
	if !valid {
		return empty, false
	}
	var snapshot PublicationSnapshot
	var checkedAt string
	var notices []json.RawMessage
	if json.Unmarshal(object["doi"], &snapshot.DOI) != nil ||
		json.Unmarshal(object["checked_at"], &checkedAt) != nil ||
		!publicationTimestampPattern.MatchString(checkedAt) ||
		json.Unmarshal(object["notices"], &notices) != nil || notices == nil || len(notices) > 100 {
		return empty, false
	}
	var err error
	snapshot.CheckedAt, err = time.Parse(time.RFC3339Nano, checkedAt)
	if err != nil {
		return empty, false
	}
	snapshot.Notices = make([]PublicationNotice, 0, len(notices))
	for _, rawNotice := range notices {
		fields, valid := publicationObject(rawNotice, []string{"doi", "type", "source"}, "date")
		if !valid {
			return empty, false
		}
		var notice PublicationNotice
		if json.Unmarshal(fields["doi"], &notice.DOI) != nil ||
			json.Unmarshal(fields["type"], &notice.Type) != nil ||
			json.Unmarshal(fields["source"], &notice.Source) != nil {
			return empty, false
		}
		if date, present := fields["date"]; present {
			if json.Unmarshal(date, &notice.Date) != nil || !publicationSnapshotDate(notice.Date) {
				return empty, false
			}
		}
		snapshot.Notices = append(snapshot.Notices, notice)
	}
	return FreezePublicationSnapshot(snapshot)
}

func publicationSnapshotDate(value string) bool {
	date, err := time.Parse("2006-01-02", value)
	return err == nil && date.Year() >= 1000 && date.Format("2006-01-02") == value
}

// FreezePublicationSnapshot validates and detaches caller-owned slices before
// persistence or readback. Provider record order and duplicates are preserved.
func FreezePublicationSnapshot(snapshot PublicationSnapshot) (PublicationSnapshot, bool) {
	var empty PublicationSnapshot
	doi, valid := NormalizeDOI(snapshot.DOI)
	_, offset := snapshot.CheckedAt.Zone()
	if !valid || doi != snapshot.DOI || snapshot.CheckedAt.Year() < 1000 ||
		snapshot.CheckedAt.Year() > 9999 || offset != 0 || snapshot.Notices == nil || len(snapshot.Notices) > 100 {
		return empty, false
	}
	frozen := PublicationSnapshot{DOI: doi, CheckedAt: snapshot.CheckedAt.UTC(), Notices: make([]PublicationNotice, len(snapshot.Notices))}
	for index, notice := range snapshot.Notices {
		noticeDOI, valid := NormalizeDOI(notice.DOI)
		kind := strings.TrimSpace(notice.Type)
		if !valid || noticeDOI != notice.DOI || !utf8.ValidString(kind) || kind != notice.Type || kind == "" || len(kind) > 100 ||
			(notice.Source != "publisher" && notice.Source != "retraction-watch" && notice.Source != "crossref") ||
			(notice.Date != "" && !publicationSnapshotDate(notice.Date)) {
			return empty, false
		}
		frozen.Notices[index] = notice
	}
	return frozen, true
}
