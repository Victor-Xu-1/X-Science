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
			DOI       string `json:"DOI"`
			UpdatedBy []struct {
				DOI     string `json:"DOI"`
				Type    string `json:"type"`
				Source  string `json:"source"`
				Updated struct {
					DateParts [][]int `json:"date-parts"`
				} `json:"updated"`
			} `json:"updated-by"`
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
	if !valid || actual != doi || len(envelope.Message.UpdatedBy) > 100 {
		return nil, false
	}
	result := make([]PublicationNotice, 0, len(envelope.Message.UpdatedBy))
	for _, item := range envelope.Message.UpdatedBy {
		noticeDOI, valid := NormalizeDOI(item.DOI)
		kind := strings.TrimSpace(item.Type)
		if !valid || kind == "" || len(kind) > 100 || (item.Source != "" && item.Source != "publisher" && item.Source != "retraction-watch") {
			return nil, false
		}
		source := item.Source
		if source == "" {
			source = "crossref"
		}
		notice := PublicationNotice{DOI: noticeDOI, Type: kind, Source: source}
		if dates := item.Updated.DateParts; len(dates) > 0 && len(dates[0]) == 3 {
			year, month, day := dates[0][0], dates[0][1], dates[0][2]
			if year >= 1000 && year <= 9999 && month >= 1 && month <= 12 && day >= 1 && day <= 31 {
				date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
				if int(date.Month()) == month && date.Day() == day {
					notice.Date = date.Format("2006-01-02")
				}
			}
		}
		result = append(result, notice)
	}
	return result, true
}
