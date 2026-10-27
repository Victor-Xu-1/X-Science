package literaturediscovery

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
)

func publicationNotices(raw []byte, doi string) ([]PublicationNotice, bool) {
	var envelope struct {
		Status  string `json:"status"`
		Type    string `json:"message-type"`
		Message struct {
			DOI       string          `json:"DOI"`
			UpdatedBy json.RawMessage `json:"updated-by"`
		} `json:"message"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&envelope) != nil || envelope.Status != "ok" || envelope.Type != "work" {
		return nil, false
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, false
	}
	actual, valid := NormalizeDOI(envelope.Message.DOI)
	if !valid || actual != doi {
		return nil, false
	}
	var updates []struct {
		DOI     string          `json:"DOI"`
		Type    string          `json:"type"`
		Source  json.RawMessage `json:"source"`
		Updated json.RawMessage `json:"updated"`
	}
	if rawUpdates := envelope.Message.UpdatedBy; len(rawUpdates) > 0 {
		// Optional provider fields may be absent, but explicit null is not an array.
		if json.Unmarshal(rawUpdates, &updates) != nil || updates == nil || len(updates) > 100 {
			return nil, false
		}
	}
	result := make([]PublicationNotice, 0, len(updates))
	for _, item := range updates {
		noticeDOI, valid := NormalizeDOI(item.DOI)
		kind := strings.TrimSpace(item.Type)
		if !valid || kind == "" || len(kind) > 100 {
			return nil, false
		}
		source := "crossref"
		if len(item.Source) > 0 {
			if json.Unmarshal(item.Source, &source) != nil || (source != "publisher" && source != "retraction-watch") {
				return nil, false
			}
		}
		date, valid := publicationNoticeDate(item.Updated)
		if !valid {
			return nil, false
		}
		result = append(result, PublicationNotice{DOI: noticeDOI, Type: kind, Source: source, Date: date})
	}
	return result, true
}

func publicationNoticeDate(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	var updated *struct {
		DateParts json.RawMessage `json:"date-parts"`
	}
	if json.Unmarshal(raw, &updated) != nil || updated == nil {
		return "", false
	}
	if len(updated.DateParts) == 0 {
		return "", true
	}
	// Pointers retain null integers; nil slices retain null arrays. Validate every
	// provider entry before projecting the first date so no malformed tail is lost.
	var dates [][]*int
	if json.Unmarshal(updated.DateParts, &dates) != nil || dates == nil {
		return "", false
	}
	for _, parts := range dates {
		if parts == nil {
			return "", false
		}
		for _, part := range parts {
			if part == nil {
				return "", false
			}
		}
	}
	if len(dates) == 0 || len(dates[0]) != 3 {
		return "", true
	}
	year, month, day := *dates[0][0], *dates[0][1], *dates[0][2]
	if year < 1000 || year > 9999 || month < 1 || month > 12 || day < 1 || day > 31 {
		return "", true
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if int(date.Month()) != month || date.Day() != day {
		return "", true
	}
	return date.Format("2006-01-02"), true
}
