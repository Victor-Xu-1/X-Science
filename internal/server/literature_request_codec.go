package server

import (
	"encoding/json"
	"io"
	"net/http"

	workspace "x-science/internal/persistence/workspace"
)

// Metadata-bearing commands also receive original pending requests from a
// capable newer writer. Keep all other mutation envelopes at their old limit.
func decodeLiteratureMutationBody(w http.ResponseWriter, r *http.Request, value any) error {
	limit := int64(64 << 10)
	switch value.(type) {
	case *workspace.LiteratureImport, *workspace.LiteraturePatch, *workspace.LiteratureManualCreate:
		limit = 128 << 10
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var trailing any
	if len(r.URL.Query()) != 0 || decoder.Decode(value) != nil || decoder.Decode(&trailing) != io.EOF {
		return workspace.ErrLiteratureInvalid
	}
	return nil
}
